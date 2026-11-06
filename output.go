package main

import (
	"fmt"
	"sort"
	"strings"
)

// Comment is attached to an inclusive range of lines of one file
// (indices into FileDiff.Lines).
type Comment struct {
	File       int
	Start, End int
	Text       string
}

// LineLabel describes a line range the way a reader would look it up:
// new-file line numbers when available, old-file numbers for removed lines.
func LineLabel(f *FileDiff, start, end int) string {
	newLo, newHi, oldLo, oldHi := 0, 0, 0, 0
	for i := start; i <= end; i++ {
		l := f.Lines[i]
		if l.NewNo > 0 {
			if newLo == 0 {
				newLo = l.NewNo
			}
			newHi = l.NewNo
		}
		if l.OldNo > 0 {
			if oldLo == 0 {
				oldLo = l.OldNo
			}
			oldHi = l.OldNo
		}
	}
	rng := func(lo, hi int) string {
		if lo == hi {
			return fmt.Sprint(lo)
		}
		return fmt.Sprintf("%d-%d", lo, hi)
	}
	switch {
	case newLo > 0:
		return rng(newLo, newHi)
	case oldLo > 0:
		return rng(oldLo, oldHi) + " (removed lines, old numbering)"
	}
	return ""
}

func commentLocation(f *FileDiff, c Comment) string {
	lbl := LineLabel(f, c.Start, c.End)
	if lbl == "" {
		return f.Path()
	}
	return f.Path() + ":" + lbl
}

const maxSnippetLines = 40

func snippet(f *FileDiff, c Comment) []string {
	var out []string
	prevHunk := -1
	for i := c.Start; i <= c.End; i++ {
		l := f.Lines[i]
		if prevHunk >= 0 && l.Hunk != prevHunk {
			out = append(out, "@@ … @@")
		}
		prevHunk = l.Hunk
		out = append(out, l.Raw)
	}
	if len(out) > maxSnippetLines {
		head := out[:maxSnippetLines-10]
		tail := out[len(out)-10:]
		omitted := len(out) - len(head) - len(tail)
		out = append(append(append([]string{}, head...), fmt.Sprintf("… (%d lines omitted)", omitted)), tail...)
	}
	return out
}

func fenceFor(lines []string) string {
	longest := 0
	for _, l := range lines {
		run := 0
		for _, r := range l {
			if r == '`' {
				run++
				longest = max(longest, run)
			} else {
				run = 0
			}
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// FormatComments renders the review as Markdown suitable for pasting into a
// Claude Code session.
func FormatComments(source string, files []*FileDiff, comments []Comment) string {
	if len(comments) == 0 {
		return ""
	}
	cs := append([]Comment(nil), comments...)
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].File != cs[j].File {
			return cs[i].File < cs[j].File
		}
		return cs[i].Start < cs[j].Start
	})

	var b strings.Builder
	fmt.Fprintf(&b, "Review comments on `%s` (%d):\n", source, len(cs))
	for _, c := range cs {
		f := files[c.File]
		lines := snippet(f, c)
		fence := fenceFor(lines)
		fmt.Fprintf(&b, "\n## %s\n\n", commentLocation(f, c))
		fmt.Fprintf(&b, "%sdiff\n%s\n%s\n\n", fence, strings.Join(lines, "\n"), fence)
		b.WriteString(strings.TrimSpace(c.Text))
		b.WriteString("\n")
	}
	return b.String()
}
