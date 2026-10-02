package diff

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Repository never writes to the work tree or to the index.
type Repository struct {
	root string
}

func Open(ctx context.Context, dir string) (*Repository, error) {
	r := &Repository{root: dir}
	output, err := r.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not in a git repository: %w", dir, err)
	}
	r.root = strings.TrimSpace(string(output))
	return r, nil
}

func (r *Repository) Root() string {
	return r.root
}

// GitFolder gives the git folder of the work tree, so a linked work tree gets a folder of its own.
func (r *Repository) GitFolder(ctx context.Context) (string, error) {
	output, err := r.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("find the git folder: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// OriginHead gives the default branch of the remote origin, such as origin/main, and false when the clone
// does not know it.
func (r *Repository) OriginHead(ctx context.Context) (string, bool) {
	output, err := r.git(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(output)), true
}

func (r *Repository) MergeBase(ctx context.Context, base string) (string, error) {
	output, err := r.git(ctx, "merge-base", "HEAD", base)
	if err != nil {
		return "", fmt.Errorf("find the merge base of HEAD and %s: %w", base, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (r *Repository) Changed(ctx context.Context, base string, pathspec Pathspec) (Lines, error) {
	output, err := r.git(ctx, append([]string{
		"-c", "core.quotePath=false",
		"diff", "--merge-base", base,
		"--unified=0", "--inter-hunk-context=0",
		"--no-color", "--no-ext-diff", "--no-textconv", "--no-relative", "--find-renames",
		"--src-prefix=a/", "--dst-prefix=b/",
		"--",
	}, pathspec.patterns()...)...)
	if err != nil {
		return Lines{}, fmt.Errorf("read the diff against %s: %w", base, err)
	}
	lines, err := r.parse(output)
	if err != nil {
		return Lines{}, err
	}

	untracked, err := r.git(ctx, append([]string{"ls-files", "--others", "--exclude-standard", "-z", "--"}, pathspec.patterns()...)...)
	if err != nil {
		return Lines{}, fmt.Errorf("list the untracked files: %w", err)
	}
	if err := r.addWholeFiles(&lines, untracked); err != nil {
		return Lines{}, err
	}
	return lines, nil
}

func (r *Repository) All(ctx context.Context, folders []string, pathspec Pathspec) (Lines, error) {
	var globs []string
	for _, folder := range folders {
		glob, err := r.folderGlob(folder)
		if err != nil {
			return Lines{}, err
		}
		globs = append(globs, glob)
	}

	files, err := r.git(ctx, append([]string{"ls-files", "--cached", "--others", "--exclude-standard", "-z", "--"}, pathspec.patterns(globs...)...)...)
	if err != nil {
		return Lines{}, fmt.Errorf("list the files in %s: %w", strings.Join(folders, ", "), err)
	}
	var lines Lines
	if err := r.addWholeFiles(&lines, files); err != nil {
		return Lines{}, err
	}
	return lines, nil
}

func (r *Repository) folderGlob(folder string) (string, error) {
	recursive := folder == "..." || strings.HasSuffix(folder, "/...")
	folder = strings.TrimSuffix(strings.TrimSuffix(folder, "..."), "/")
	if folder == "" {
		folder = "."
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s is not a folder: --all takes the folders of packages", folder)
	}
	absolute, err := filepath.Abs(folder)
	if err == nil {
		absolute, err = filepath.EvalSymlinks(absolute)
	}
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(r.root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is not in the repository at %s", folder, r.root)
	}
	if recursive {
		return filepath.ToSlash(filepath.Join(relative, "**")), nil
	}
	return filepath.ToSlash(relative), nil
}

// parse needs a diff with --unified=0 and the prefix b/.
func (r *Repository) parse(output []byte) (Lines, error) {
	var lines Lines
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	file := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file = ""
		case strings.HasPrefix(line, "+++ "):
			name, err := r.unquote(strings.TrimPrefix(line, "+++ "))
			if err != nil {
				return Lines{}, fmt.Errorf("read the file name in %q: %w", line, err)
			}
			file = strings.TrimPrefix(name, "b/")
			if name == "/dev/null" {
				file = ""
			}
		case strings.HasPrefix(line, "@@ "):
			removed, added, first, err := r.hunk(line)
			if err != nil {
				return Lines{}, err
			}
			if file != "" {
				lines.Add(file, first, first+added-1)
			}
			for removed > 0 || added > 0 {
				if !scanner.Scan() {
					return Lines{}, fmt.Errorf("the hunk %q ends before its last line", line)
				}
				switch body := scanner.Text(); {
				case strings.HasPrefix(body, "-"):
					removed--
				case strings.HasPrefix(body, "+"):
					added--
				case strings.HasPrefix(body, " "):
					removed--
					added--
				}
			}
		}
	}
	return lines, scanner.Err()
}

func (r *Repository) hunk(header string) (removed, added, first int, err error) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, 0, fmt.Errorf("read the hunk header %q", header)
	}
	_, removed, err = r.lineRange(strings.TrimPrefix(fields[1], "-"))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read the hunk header %q: %w", header, err)
	}
	first, added, err = r.lineRange(strings.TrimPrefix(fields[2], "+"))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read the hunk header %q: %w", header, err)
	}
	return removed, added, first, nil
}

func (r *Repository) lineRange(text string) (first, count int, err error) {
	start, length, found := strings.Cut(text, ",")
	if first, err = strconv.Atoi(start); err != nil {
		return 0, 0, err
	}
	// git leaves out the count when it is 1
	if !found {
		return first, 1, nil
	}
	count, err = strconv.Atoi(length)
	return first, count, err
}

// git quotes a name that has a quote, a backslash or a control character, and ends a name that has a
// space with a tab
func (r *Repository) unquote(name string) (string, error) {
	if !strings.HasPrefix(name, `"`) {
		return strings.TrimSuffix(name, "\t"), nil
	}
	return strconv.Unquote(name)
}

func (r *Repository) addWholeFiles(lines *Lines, list []byte) error {
	for name := range strings.SplitSeq(string(list), "\x00") {
		if name == "" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(r.root, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		count := bytes.Count(content, []byte("\n"))
		if len(content) > 0 && content[len(content)-1] != '\n' {
			count++
		}
		lines.Add(name, 1, count)
	}
	return nil
}

func (r *Repository) git(ctx context.Context, arguments ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "git", append([]string{"-C", r.root}, arguments...)...).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return nil, errors.New(strings.TrimSpace(string(exit.Stderr)))
	}
	return output, err
}
