package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const sample = `diff --git a/n.txt b/n.txt
index 1c99002..ba9a42f 100644
--- a/n.txt
+++ b/n.txt
@@ -2,7 +2,7 @@ func x()
 2
 3
 4
--- 5
+five
 6
 7
 8
@@ -27,3 +27,4 @@
 27
-28
+twenty-eight
+28.5
 29
diff --git a/nonl.txt b/nonl.txt
index c1b0730..e25f181 100644
--- a/nonl.txt
+++ b/nonl.txt
@@ -1 +1 @@
-x
\ No newline at end of file
+y
\ No newline at end of file
diff --git a/pkg/foo_test.go b/pkg/foo_test.go
new file mode 100644
index 0000000..3e75765
--- /dev/null
+++ b/pkg/foo_test.go
@@ -0,0 +1 @@
+package pkg
diff --git a/old name.txt b/new name.txt
similarity index 100%
rename from old name.txt
rename to new name.txt
diff --git a/img.png b/img.png
index 1111111..2222222 100644
Binary files a/img.png and b/img.png differ
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
`

func TestParseDiff(t *testing.T) {
	files := ParseDiff(sample)
	if len(files) != 6 {
		t.Fatalf("got %d files", len(files))
	}
	n := files[0]
	if n.Path() != "n.txt" || len(n.Hunks) != 2 || n.Added != 3 || n.Removed != 2 {
		t.Fatalf("n.txt parsed wrong: %+v", n)
	}
	// "--- 5" inside a hunk is a removed line, not a file header
	l := n.Lines[3]
	if l.Kind != Removed || l.OldNo != 5 || l.NewNo != 0 || l.Raw != "--- 5" {
		t.Fatalf("removed line wrong: %+v", l)
	}
	l = n.Lines[4]
	if l.Kind != Added || l.NewNo != 5 {
		t.Fatalf("added line wrong: %+v", l)
	}
	if got := n.Lines[n.Hunks[1].Start+3]; got.Kind != Added || got.NewNo != 29 {
		t.Fatalf("second hunk numbering wrong: %+v", got)
	}

	nonl := files[1]
	if len(nonl.Lines) != 4 || nonl.Lines[1].Kind != NoNewline || nonl.Lines[3].Kind != NoNewline {
		t.Fatalf("no-newline handling wrong: %+v", nonl.Lines)
	}

	if f := files[2]; f.Status != "added" || f.Path() != "pkg/foo_test.go" || !f.IsTest {
		t.Fatalf("new file wrong: %+v", f)
	}
	if f := files[3]; f.Status != "renamed" || f.OldPath != "old name.txt" || f.NewPath != "new name.txt" || len(f.Notes) != 1 {
		t.Fatalf("rename wrong: %+v", f)
	}
	if f := files[4]; !f.Binary || f.Path() != "img.png" {
		t.Fatalf("binary wrong: %+v", f)
	}
	if f := files[5]; f.Path() != "run.sh" || len(f.Notes) != 1 || f.Notes[0] != "Mode changed from 100644 to 100755" {
		t.Fatalf("mode change wrong: %+v", f)
	}

	sorted := SortTestsLast(files)
	if sorted[len(sorted)-1].Path() != "pkg/foo_test.go" || sorted[0].Path() != "n.txt" {
		t.Fatalf("tests not sorted last")
	}
}

func TestHashStable(t *testing.T) {
	a := ParseDiff(sample)
	b := ParseDiff(strings.Replace(sample, "index 1c99002..ba9a42f", "index aaaaaaa..bbbbbbb", 1))
	if a[0].Hash != b[0].Hash {
		t.Fatal("hash should ignore index line")
	}
	c := ParseDiff(strings.Replace(sample, "+five", "+FIVE", 1))
	if a[0].Hash == c[0].Hash {
		t.Fatal("hash should change with content")
	}
}

func TestIsTestPath(t *testing.T) {
	yes := []string{"foo_test.go", "tests/a.py", "src/__tests__/x.js", "FooTest.java", "testdata/x", "test_utils.py", "a.test.ts", "TestHelpers.cs"}
	no := []string{"latest.go", "attestation.rs", "contest/main.c", "src/main.go"}
	for _, p := range yes {
		if !isTestPath(p) {
			t.Errorf("%s should be a test path", p)
		}
	}
	for _, p := range no {
		if isTestPath(p) {
			t.Errorf("%s should not be a test path", p)
		}
	}
}

func TestQuotedPaths(t *testing.T) {
	d := "diff --git \"a/caf\\303\\251.txt\" \"b/caf\\303\\251.txt\"\nnew file mode 100644\n--- /dev/null\n+++ \"b/caf\\303\\251.txt\"\n@@ -0,0 +1 @@\n+x\n"
	f := ParseDiff(d)
	if len(f) != 1 || f[0].Path() != "café.txt" {
		t.Fatalf("got %+v", f)
	}
}

func TestParseDeltaLine(t *testing.T) {
	h := parseDeltaLine("\x1b[48;2;0;40;0;38;2;249;38;114mimport\x1b[38;2;248;248;242m x\x1b[0m\x1b[48;2;0;40;0m\x1b[0K\x1b[0m")
	if h.Fill != "\x1b[48;2;0;40;0m" || ansi.Strip(h.Text) != "import x" || h.Width != 8 {
		t.Fatalf("got %+v", h)
	}
	h = parseDeltaLine("\x1b[48;2;0;40;0m\x1b[0K\x1b[0m")
	if h.Fill != "\x1b[48;2;0;40;0m" || h.Width != 0 {
		t.Fatalf("empty: got %+v", h)
	}
}

func TestDeltaMapping(t *testing.T) {
	if _, err := exec.LookPath("delta"); err != nil {
		t.Skip("delta not installed")
	}
	for _, f := range ParseDiff(sample) {
		if len(f.Hunks) == 0 {
			continue
		}
		hl, err := HighlightWithDelta("delta", f)
		if err != nil {
			t.Fatalf("%s: %v", f.Path(), err)
		}
		for i, h := range hl {
			want := strings.TrimRight(sanitize(f.Lines[i].Content()), " ")
			if f.Lines[i].Kind == NoNewline {
				want = sanitize(f.Lines[i].Raw)
			}
			if got := strings.TrimRight(ansi.Strip(h.Text), " "); got != want {
				t.Errorf("%s line %d: got %q want %q", f.Path(), i, got, want)
			}
		}
	}
}

func TestFormatComments(t *testing.T) {
	files := ParseDiff(sample)
	out := FormatComments("git diff", files, []Comment{
		{File: 0, Start: 3, End: 4, Text: "  rename this  "},
		{File: 1, Start: 0, End: 0, Text: "why?"},
	})
	for _, want := range []string{
		"Review comments on `git diff` (2):",
		"## n.txt:5\n",
		"```diff\n--- 5\n+five\n```\n\nrename this\n",
		"## nonl.txt:1 (removed lines, old numbering)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrefixVariants(t *testing.T) {
	body := "index 1..2 100644\n--- %s\n+++ %s\n@@ -1 +1 @@\n-x\n+y\n"
	cases := []struct{ git, minus, plus string }{
		{"a/src/a.go b/src/a.go", "a/src/a.go", "b/src/a.go"},
		{"i/src/a.go w/src/a.go", "i/src/a.go", "w/src/a.go"}, // diff.mnemonicPrefix
		{"src/a.go src/a.go", "src/a.go", "src/a.go"},         // diff.noprefix
	}
	for _, c := range cases {
		d := "diff --git " + c.git + "\n" + fmt.Sprintf(body, c.minus, c.plus)
		f := ParseDiff(d)
		if len(f) != 1 || f[0].Path() != "src/a.go" || f[0].OldPath != "src/a.go" || f[0].Malformed {
			t.Errorf("%q: got %+v", c.git, f[0])
		}
	}
	// noprefix with a top-level directory called "a" must not be stripped
	f := ParseDiff("diff --git a/x.go a/x.go\n--- a/x.go\n+++ a/x.go\n@@ -1 +1 @@\n-x\n+y\n")
	if f[0].Path() != "a/x.go" {
		t.Errorf("noprefix a/: got %q", f[0].Path())
	}
	// mnemonic prefixes on a mode-only change (no ---/+++ lines)
	f = ParseDiff("diff --git i/run.sh w/run.sh\nold mode 100644\nnew mode 100755\n")
	if f[0].Path() != "run.sh" {
		t.Errorf("mode change: got %q", f[0].Path())
	}
}

func TestMalformed(t *testing.T) {
	wordDiff := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\nfoo [-bar-]{+baz+}\n same\n"
	if f := ParseDiff(wordDiff); len(f) != 1 || !f[0].Malformed {
		t.Fatalf("word diff should be malformed: %+v", f)
	}
	if f := ParseDiff(sample); f[0].Malformed || f[1].Malformed {
		t.Fatal("sample should parse cleanly")
	}
	if f := ParseDiff(" README.md | 3 ++-\n 1 file changed\n"); len(f) != 0 {
		t.Fatal("stat output is not a diff")
	}
}

func TestColoredInput(t *testing.T) {
	colored := "\x1b[1mdiff --git a/x b/x\x1b[m\n\x1b[1m--- a/x\x1b[m\n\x1b[1m+++ b/x\x1b[m\n\x1b[36m@@ -1 +1 @@\x1b[m\n\x1b[31m-x\x1b[m\n\x1b[32m+y\x1b[m\n"
	f := ParseDiff(ansi.Strip(colored))
	if len(f) != 1 || f[0].Path() != "x" || f[0].Added != 1 || f[0].Malformed {
		t.Fatalf("got %+v", f)
	}
}
