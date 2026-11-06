package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// Highlighted is the rendered form of a single diff line.
type Highlighted struct {
	Text  string
	Fill  string
	Width int
}

// deltaCommand returns the shell snippet used to invoke delta. It honours
// $DELTA_REVIEW_DELTA, then git's configured pager (if it is delta or a
// wrapper around it), then plain `delta` from $PATH.
func deltaCommand() string {
	if v := strings.TrimSpace(os.Getenv("DELTA_REVIEW_DELTA")); v != "" {
		return v
	}
	if out, err := exec.Command("git", "var", "GIT_PAGER").Output(); err == nil {
		p := strings.TrimSpace(string(out))
		if strings.Contains(p, "delta") && !strings.Contains(p, "delta-review") {
			return p
		}
	}
	if _, err := exec.LookPath("delta"); err == nil {
		return "delta"
	}
	return ""
}

var deltaArgs = []string{
	"--file-style=omit",
	"--hunk-header-style=omit",
	"--line-fill-method=ansi",
	"--paging=never",
	"--width=1000",
}

// HighlightWithDelta runs delta over one file's diff and maps its output back
// onto the diff lines. With file and hunk headers omitted, delta prints one
// blank separator per hunk followed by exactly one line per diff line.
func HighlightWithDelta(cmdline string, f *FileDiff) ([]Highlighted, error) {
	if len(f.Hunks) == 0 || f.Combined {
		return nil, fmt.Errorf("nothing to highlight")
	}
	args := append([]string{"-c", cmdline + ` "$@"`, "delta-review"}, deltaArgs...)
	cmd := exec.Command("sh", args...)
	cmd.Stdin = strings.NewReader(strings.Join(f.Raw, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("delta: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	text := strings.TrimSuffix(string(out), "\n")
	outLines := strings.Split(text, "\n")

	expected := len(f.Hunks) + len(f.Lines)
	if len(outLines) != expected {
		return nil, fmt.Errorf("delta produced %d lines, expected %d", len(outLines), expected)
	}
	res := make([]Highlighted, 0, len(f.Lines))
	pos := 0
	for _, h := range f.Hunks {
		if strings.TrimSpace(ansi.Strip(outLines[pos])) != "" {
			return nil, fmt.Errorf("unexpected hunk separator %q", ansi.Strip(outLines[pos]))
		}
		pos++
		for i := h.Start; i < h.End; i++ {
			res = append(res, parseDeltaLine(outLines[pos]))
			pos++
		}
	}
	return res, nil
}

// parseDeltaLine strips delta's "erase to end of line" fill and remembers the
// style that was active, so rows can be padded to any width later on.
func parseDeltaLine(s string) Highlighted {
	s = strings.TrimRight(s, "\r")
	fill := ""
	for _, el := range []string{"\x1b[0K", "\x1b[K"} {
		if i := strings.LastIndex(s, el); i >= 0 {
			before := s[:i]
			if j := strings.LastIndex(before, "\x1b["); j >= 0 && strings.HasSuffix(before, "m") {
				fill = before[j:]
			}
			s = before
			break
		}
	}
	s = strings.TrimSuffix(s, "\x1b[0m")
	if fill != "" {
		s = strings.TrimSuffix(s, fill)
		s = strings.TrimSuffix(s, "\x1b[0m")
	}
	return Highlighted{Text: s, Fill: fill, Width: ansi.StringWidth(s)}
}

// PlainHighlight is the fallback used until (or instead of) delta output.
func PlainHighlight(l *DiffLine) Highlighted {
	text := sanitize(l.Content())
	switch l.Kind {
	case Added:
		return Highlighted{Text: "\x1b[32m" + text, Width: ansi.StringWidth(text)}
	case Removed:
		return Highlighted{Text: "\x1b[31m" + text, Width: ansi.StringWidth(text)}
	case NoNewline:
		t := "\x1b[2m" + sanitize(l.Raw)
		return Highlighted{Text: t, Width: ansi.StringWidth(sanitize(l.Raw))}
	}
	return Highlighted{Text: text, Width: ansi.StringWidth(text)}
}

func sanitize(s string) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case r < 0x20 || r == 0x7f:
			b.WriteRune('�')
			col++
		default:
			b.WriteRune(r)
			col += ansi.StringWidth(string(r))
		}
	}
	return b.String()
}

type highlightMsg struct {
	file  int
	lines []Highlighted
	err   error
}

// highlightAll highlights files in display order using a small worker pool,
// delivering each result through send.
func highlightAll(cmdline string, files []*FileDiff, send func(highlightMsg)) {
	if cmdline == "" {
		return
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := min(runtime.NumCPU(), 8)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lines, err := HighlightWithDelta(cmdline, files[i])
				send(highlightMsg{file: i, lines: lines, err: err})
			}
		}()
	}
	for i, f := range files {
		if len(f.Hunks) > 0 && !f.Combined {
			jobs <- i
		}
	}
	close(jobs)
	wg.Wait()
}
