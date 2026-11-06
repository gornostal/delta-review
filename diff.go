package main

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type LineKind int

const (
	Context LineKind = iota
	Added
	Removed
	NoNewline // "\ No newline at end of file"
)

type DiffLine struct {
	Kind  LineKind
	OldNo int // 0 when the line does not exist on the old side
	NewNo int // 0 when the line does not exist on the new side
	Hunk  int // index of the hunk this line belongs to
	Raw   string

	// rendering
	Text  string // ANSI-coloured content (no trailing reset/fill)
	Fill  string // SGR sequence used to pad the rest of the row ("" = none)
	Width int    // visible width of Text
}

func (l *DiffLine) Content() string {
	if len(l.Raw) == 0 {
		return ""
	}
	return l.Raw[1:]
}

type Hunk struct {
	Header string
	Start  int // index of the first line in FileDiff.Lines
	End    int // exclusive
}

type FileDiff struct {
	OldPath, NewPath string
	Status           string // modified, added, deleted, renamed, copied
	Binary           bool
	Combined         bool
	Notes            []string // things to show when there is no textual diff (mode change, binary...)
	Raw              []string // all raw lines of this file's diff, header included
	Hunks            []Hunk
	Lines            []DiffLine // all hunk lines, flattened
	Added, Removed   int
	Hash             string
	IsTest           bool
	// Malformed is set when hunk bodies don't match their headers, e.g. for
	// --word-diff output, which can't be reviewed line by line.
	Malformed bool

	// raw path tokens, resolved once the whole header has been read
	gitA, gitB          string // from "diff --git A B"
	minusPath, plusPath string // from "--- " / "+++ "
	explicitPaths       bool   // rename/copy from/to lines gave exact paths
	binaryPatch         bool   // "GIT binary patch": data lines follow the header
}

func (f *FileDiff) Path() string {
	if f.Status == "deleted" || f.NewPath == "" {
		return f.OldPath
	}
	return f.NewPath
}

func (f *FileDiff) DisplayPath() string {
	if (f.Status == "renamed" || f.Status == "copied") && f.OldPath != f.NewPath && f.OldPath != "" {
		return f.OldPath + " → " + f.NewPath
	}
	return f.Path()
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff parses unified diff output (as produced by git diff) into files.
func ParseDiff(text string) []*FileDiff {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")

	var files []*FileDiff
	var cur *FileDiff
	inHunk := false
	oldLeft, newLeft := 0, 0
	oldNo, newNo := 0, 0

	finish := func() {
		if cur == nil {
			return
		}
		if n := len(cur.Hunks); n > 0 {
			cur.Hunks[n-1].End = len(cur.Lines)
		}
		if inHunk && !cur.Combined && (oldLeft > 0 || newLeft > 0) {
			cur.Malformed = true
		}
		inHunk = false
		files = append(files, cur)
		cur = nil
	}

	for i := 0; i < len(lines); i++ {
		l := lines[i]

		if inHunk {
			if cur.Combined {
				if l == "" || !strings.ContainsRune(" +-", rune(l[0])) {
					// next file or hunk, or the next commit of `git show A B`
					inHunk = false
				} else {
					cur.Raw = append(cur.Raw, l)
					cur.Lines = append(cur.Lines, DiffLine{Kind: Context, Hunk: len(cur.Hunks) - 1, Raw: " " + l})
					continue
				}
			} else {
				kind := Context
				c := byte(' ')
				if len(l) > 0 {
					c = l[0]
				}
				switch c {
				case '+':
					kind = Added
				case '-':
					kind = Removed
				case '\\':
					kind = NoNewline
				case ' ':
				default:
					// malformed; end the hunk and reprocess the line
					cur.Malformed = true
					inHunk = false
					i--
					continue
				}
				dl := DiffLine{Kind: kind, Hunk: len(cur.Hunks) - 1, Raw: l}
				if len(l) == 0 {
					dl.Raw = " "
				}
				switch kind {
				case Context:
					dl.OldNo, dl.NewNo = oldNo, newNo
					oldNo++
					newNo++
					oldLeft--
					newLeft--
				case Added:
					dl.NewNo = newNo
					newNo++
					newLeft--
					cur.Added++
				case Removed:
					dl.OldNo = oldNo
					oldNo++
					oldLeft--
					cur.Removed++
				}
				cur.Raw = append(cur.Raw, l)
				cur.Lines = append(cur.Lines, dl)
				if oldLeft <= 0 && newLeft <= 0 {
					// hunk complete, but allow a trailing "\ No newline" marker
					if i+1 < len(lines) && strings.HasPrefix(lines[i+1], `\`) {
						continue
					}
					inHunk = false
				}
				continue
			}
		}

		switch {
		case strings.HasPrefix(l, "diff --git "):
			finish()
			cur = &FileDiff{Status: "modified"}
			cur.gitA, cur.gitB = splitGitDiffLine(strings.TrimPrefix(l, "diff --git "))
		case strings.HasPrefix(l, "diff --cc ") || strings.HasPrefix(l, "diff --combined "):
			finish()
			p := l[strings.Index(l[5:], " ")+6:]
			cur = &FileDiff{Status: "modified", Combined: true, OldPath: unquote(p), NewPath: unquote(p)}
		case cur != nil && strings.HasPrefix(l, "@@@"):
			if n := len(cur.Hunks); n > 0 {
				cur.Hunks[n-1].End = len(cur.Lines)
			}
			cur.Hunks = append(cur.Hunks, Hunk{Header: l, Start: len(cur.Lines)})
			inHunk = true
		case cur != nil && strings.HasPrefix(l, "@@"):
			if n := len(cur.Hunks); n > 0 {
				cur.Hunks[n-1].End = len(cur.Lines)
			}
			m := hunkRe.FindStringSubmatch(l)
			if m == nil {
				cur.Malformed = true
				break
			}
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[3])
			oldLeft, newLeft = 1, 1
			if m[2] != "" {
				oldLeft, _ = strconv.Atoi(m[2])
			}
			if m[4] != "" {
				newLeft, _ = strconv.Atoi(m[4])
			}
			cur.Hunks = append(cur.Hunks, Hunk{Header: l, Start: len(cur.Lines)})
			inHunk = oldLeft > 0 || newLeft > 0
		case strings.HasPrefix(l, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") &&
			(cur == nil || len(cur.Hunks) > 0):
			// plain unified diff without a "diff --git" line
			finish()
			cur = &FileDiff{Status: "modified"}
		case cur != nil && !cur.binaryPatch && (len(cur.Hunks) > 0 || !isHeaderLine(l)):
			// not part of this file, e.g. the next commit's header in `git show A B`
			finish()
			continue
		case cur == nil:
			continue // preamble (e.g. commit message from git show)
		}

		if cur == nil {
			continue
		}
		if !strings.HasPrefix(l, "@@") || len(cur.Hunks) == 0 {
			parseHeaderLine(cur, l)
		}
		cur.Raw = append(cur.Raw, l)
	}
	finish()

	for _, f := range files {
		resolvePaths(f)
		if len(f.Hunks) == 0 && len(f.Notes) == 0 {
			switch f.Status {
			case "renamed":
				f.Notes = append(f.Notes, "File renamed without changes")
			case "copied":
				f.Notes = append(f.Notes, "File copied without changes")
			case "added":
				f.Notes = append(f.Notes, "Empty file added")
			case "deleted":
				f.Notes = append(f.Notes, "Empty file deleted")
			}
		}
		f.Hash = hashFile(f)
		f.IsTest = isTestPath(f.Path())
	}
	return files
}

func parseHeaderLine(f *FileDiff, l string) {
	switch {
	case strings.HasPrefix(l, "--- "):
		f.minusPath = unquote(cutTab(l[4:]))
		if f.minusPath == "/dev/null" {
			f.Status = "added"
		}
	case strings.HasPrefix(l, "+++ "):
		f.plusPath = unquote(cutTab(l[4:]))
		if f.plusPath == "/dev/null" {
			f.Status = "deleted"
		}
	case strings.HasPrefix(l, "new file mode"):
		f.Status = "added"
	case strings.HasPrefix(l, "deleted file mode"):
		f.Status = "deleted"
	case strings.HasPrefix(l, "rename from "):
		f.Status = "renamed"
		f.OldPath = unquote(l[len("rename from "):])
		f.explicitPaths = true
	case strings.HasPrefix(l, "rename to "):
		f.Status = "renamed"
		f.NewPath = unquote(l[len("rename to "):])
		f.explicitPaths = true
	case strings.HasPrefix(l, "copy from "):
		f.Status = "copied"
		f.OldPath = unquote(l[len("copy from "):])
		f.explicitPaths = true
	case strings.HasPrefix(l, "copy to "):
		f.Status = "copied"
		f.NewPath = unquote(l[len("copy to "):])
		f.explicitPaths = true
	case strings.HasPrefix(l, "old mode "):
		f.Notes = append(f.Notes, "Mode changed from "+l[len("old mode "):])
	case strings.HasPrefix(l, "new mode "):
		if n := len(f.Notes); n > 0 && strings.HasPrefix(f.Notes[n-1], "Mode changed from ") {
			f.Notes[n-1] += " to " + l[len("new mode "):]
		}
	case strings.HasPrefix(l, "Binary files ") || strings.HasPrefix(l, "GIT binary patch"):
		f.Binary = true
		f.binaryPatch = strings.HasPrefix(l, "GIT binary patch")
		f.Notes = append(f.Notes, "Binary file changed")
	}
}

// headerPrefixes are the lines that can appear between a file's "diff" line
// and its first hunk.
var headerPrefixes = []string{
	"diff ", "--- ", "+++ ", "index ", "old mode ", "new mode ", "new file mode ",
	"deleted file mode ", "similarity index ", "dissimilarity index ", "rename from ",
	"rename to ", "copy from ", "copy to ", "Binary files ", "GIT binary patch",
}

func isHeaderLine(l string) bool {
	for _, p := range headerPrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// splitGitDiffLine splits the operands of `diff --git A B` (possibly quoted)
// without interpreting prefixes.
func splitGitDiffLine(s string) (string, string) {
	if strings.HasPrefix(s, `"`) || strings.HasSuffix(s, `"`) {
		if toks := splitQuoted(s); len(toks) == 2 {
			return toks[0], toks[1]
		}
	}
	// same path on both sides: "a/X b/X" or, without prefixes, "X X"
	if (len(s)-1)%2 == 0 {
		half := (len(s) - 1) / 2
		a, b := s[:half], s[half+1:]
		if s[half] == ' ' && (a == b || (len(a) > 2 && a[1] == '/' && b[1] == '/' && a[2:] == b[2:])) {
			return a, b
		}
	}
	// different paths with one-letter prefixes: "a/old b/new", "i/old w/new"
	if len(s) > 2 && s[1] == '/' {
		for i := 2; i+3 < len(s); i++ {
			if s[i] == ' ' && s[i+2] == '/' {
				return s[:i], s[i+1:]
			}
		}
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, s
}

// detectPrefixes works out the source/destination prefixes git used: a/ b/
// by default, i/ w/ etc. with diff.mnemonicPrefix, none with diff.noprefix.
func detectPrefixes(a, b string) (string, string) {
	if a == "" && b == "" {
		return "a/", "b/" // plain unified diff
	}
	if a == b {
		return "", ""
	}
	if len(a) > 2 && len(b) > 2 && a[1] == '/' && b[1] == '/' {
		return a[:2], b[:2]
	}
	return "", ""
}

func resolvePaths(f *FileDiff) {
	if f.explicitPaths && f.OldPath != "" && f.NewPath != "" {
		return
	}
	pa, pb := detectPrefixes(f.gitA, f.gitB)
	if f.OldPath == "" {
		switch {
		case f.minusPath != "" && f.minusPath != "/dev/null":
			f.OldPath = strings.TrimPrefix(f.minusPath, pa)
		case f.gitA != "":
			f.OldPath = strings.TrimPrefix(f.gitA, pa)
		}
	}
	if f.NewPath == "" {
		switch {
		case f.plusPath != "" && f.plusPath != "/dev/null":
			f.NewPath = strings.TrimPrefix(f.plusPath, pb)
		case f.gitB != "":
			f.NewPath = strings.TrimPrefix(f.gitB, pb)
		}
	}
	if f.Status == "deleted" && f.OldPath == "" {
		f.OldPath = f.NewPath
	}
}

func splitQuoted(s string) []string {
	var out []string
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ")
		if s == "" {
			break
		}
		if s[0] == '"' {
			end := 1
			for end < len(s) && s[end] != '"' {
				if s[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(s) {
				return nil
			}
			out = append(out, unquote(s[:end+1]))
			s = s[end+1:]
		} else {
			i := strings.IndexByte(s, ' ')
			if i < 0 {
				out = append(out, s)
				break
			}
			out = append(out, s[:i])
			s = s[i+1:]
		}
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

func cutTab(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 && !strings.HasPrefix(s, `"`) {
		return s[:i]
	}
	return s
}

// hashFile identifies a file's diff independently of blob ids, so that the
// "viewed" mark survives as long as the change itself stays the same.
func hashFile(f *FileDiff) string {
	h := sha256.New()
	h.Write([]byte(f.OldPath + "\x00" + f.NewPath + "\x00" + f.Status + "\x00"))
	for _, n := range f.Notes {
		h.Write([]byte(n + "\n"))
	}
	for _, hk := range f.Hunks {
		h.Write([]byte(hk.Header + "\n"))
		for _, l := range f.Lines[hk.Start:hk.End] {
			h.Write([]byte(l.Raw + "\n"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// isTestPath reports whether a path contains the word "test" (test, tests,
// testing, testdata, FooTest, foo_test, __tests__, ...), but not words that
// merely contain the letters, like "latest" or "attestation".
func isTestPath(p string) bool {
	for _, w := range splitWords(p) {
		if strings.HasPrefix(strings.ToLower(w), "test") {
			return true
		}
	}
	return false
}

func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prevLower := unicode.IsLower(cur[len(cur)-1])
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if prevLower || (nextLower && unicode.IsUpper(cur[len(cur)-1])) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// SortTestsLast moves test files after everything else, keeping relative order.
func SortTestsLast(files []*FileDiff) []*FileDiff {
	out := make([]*FileDiff, 0, len(files))
	for _, f := range files {
		if !f.IsTest {
			out = append(out, f)
		}
	}
	for _, f := range files {
		if f.IsTest {
			out = append(out, f)
		}
	}
	return out
}
