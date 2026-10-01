package progress

import (
	"fmt"
	"io"
)

const (
	toLineStart     = "\r"
	clearRestOfLine = "\x1b[K"
)

type Line struct {
	w        io.Writer
	terminal bool
	label    string
	shown    string
}

func Start(w io.Writer, terminal bool, label string) *Line {
	l := &Line{w: w, terminal: terminal, label: label}
	if terminal {
		l.draw(label + "…")
	} else {
		fmt.Fprintln(w, label+"…")
	}
	return l
}

func (l *Line) Bytes(done, total int64) {
	if !l.terminal {
		return
	}
	text := l.label + ": " + size(done)
	if total > 0 {
		text += fmt.Sprintf(" of %s (%d%%)", size(total), done*100/total)
	}
	l.draw(text)
}

func (l *Line) Count(done, total int) {
	if !l.terminal {
		return
	}
	l.draw(fmt.Sprintf("%s: %d of %d", l.label, done, total))
}

func (l *Line) End() {
	if l.terminal {
		fmt.Fprintln(l.w)
	}
}

func (l *Line) draw(text string) {
	if text == l.shown {
		return
	}
	fmt.Fprint(l.w, toLineStart+text+clearRestOfLine)
	l.shown = text
}

func size(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f KB", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1000/1000)
	}
}
