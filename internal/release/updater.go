package release

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	binaryName    = "mutants"
	checksumsName = "checksums.txt"
)

type releases interface {
	Latest(ctx context.Context) (Release, error)
	LatestPrerelease(ctx context.Context) (Release, error)
	Download(ctx context.Context, a Asset, progress func(done, total int64)) ([]byte, error)
}

type Channel string

const (
	Releases    Channel = "release"
	Prereleases Channel = "prerelease"
)

type Check struct {
	Current  string
	Latest   Release
	UpToDate bool
}

type Updater struct {
	releases   releases
	current    string
	platform   string
	executable string
}

func NewUpdater(releases releases, current, platform, executable string) *Updater {
	return &Updater{releases: releases, current: current, platform: platform, executable: executable}
}

func (u *Updater) Check(ctx context.Context, channel Channel) (Check, error) {
	latest := u.releases.Latest
	if channel == Prereleases {
		latest = u.releases.LatestPrerelease
	}
	r, err := latest(ctx)
	if err != nil {
		return Check{}, err
	}
	return Check{Current: u.current, Latest: r, UpToDate: r.Tag == u.current}, nil
}

func (u *Updater) Install(ctx context.Context, r Release, progress func(done, total int64)) error {
	archiveName := binaryName + "-" + u.platform + ".tar.gz"
	archiveAsset, ok := r.Asset(archiveName)
	if !ok {
		return fmt.Errorf("release %s has no %s for %s", r.Tag, archiveName, u.platform)
	}
	checksumsAsset, ok := r.Asset(checksumsName)
	if !ok {
		return fmt.Errorf("release %s has no %s to check the download against", r.Tag, checksumsName)
	}
	archive, err := u.releases.Download(ctx, archiveAsset, progress)
	if err != nil {
		return err
	}
	checksums, err := u.releases.Download(ctx, checksumsAsset, nil)
	if err != nil {
		return err
	}
	if err := checkSum(archive, archiveName, checksums); err != nil {
		return err
	}
	binary, err := extract(archive, binaryName)
	if err != nil {
		return fmt.Errorf("read %s: %w", archiveName, err)
	}
	return replaceAtomically(u.executable, binary)
}

func checkSum(data []byte, name string, checksums []byte) error {
	sum := sha256.Sum256(data)
	scanner := bufio.NewScanner(bytes.NewReader(checksums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == name {
			if fields[0] != hex.EncodeToString(sum[:]) {
				return fmt.Errorf("%s does not match its checksum; the download is not installed", name)
			}
			return nil
		}
	}
	return fmt.Errorf("%s has no line for %s", checksumsName, name)
}

func extract(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	files := tar.NewReader(gz)
	for {
		header, err := files.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s in the archive", name)
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == name {
			return io.ReadAll(io.LimitReader(files, maxDownload))
		}
	}
}

func replaceAtomically(path string, binary []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+binaryName+"-update-*")
	if err != nil {
		return fmt.Errorf("write beside %s: %w", path, err)
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(binary); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
