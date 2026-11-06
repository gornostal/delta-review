package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
)

const usage = `delta-review — review a git diff, leave line comments, print them on exit.

Usage:
  delta-review [git diff args...]     e.g. delta-review --cached, delta-review main...HEAD
  delta-review show [git show args...]  review commits, e.g. delta-review show HEAD~2
  git diff ... | delta-review         review a diff read from stdin

Works as a git pager too:  git config --global pager.diff delta-review
                           git config --global pager.show delta-review
Input that isn't a plain diff (--stat, --word-diff, ...) is passed on to
git's regular pager.

On q the comments are printed to stdout as Markdown and copied to the
clipboard, ready to paste into Claude Code.

Environment:
  DELTA_REVIEW_DELTA      command used to highlight (default: git's pager if
                          it is delta, else delta from $PATH)
  DELTA_REVIEW_CLIPBOARD  command that receives the review on stdin (default:
                          wl-copy, xclip, xsel or pbcopy, else OSC 52)

Press ? inside the app for key bindings.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "delta-review:", err)
		os.Exit(1)
	}
}

func isTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

func run(args []string) error {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Print(usage)
			return nil
		}
	}

	var diffText, source string
	var rawInput []byte // original stdin, for handing non-diff input to the regular pager
	pagerMode := !isTTY(os.Stdin) && len(args) == 0
	if pagerMode {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil // e.g. `git diff` with no changes
		}
		// git colours its output when a pager is in use
		rawInput, diffText = data, ansi.Strip(string(data))
		source = gitParentCommand()
		if source == "" {
			source = "stdin diff"
		}
	} else {
		// `delta-review show [args]` reviews commits; anything else is git diff args
		sub := "diff"
		if len(args) > 0 && args[0] == "show" {
			sub, args = "show", args[1:]
		}
		gitArgs := append([]string{"-c", "core.quotePath=false", sub, "--no-color", "--no-ext-diff",
			"--src-prefix=a/", "--dst-prefix=b/"}, args...)
		cmd := exec.Command("git", gitArgs...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("git %s failed: %v\n%s", sub, err, strings.TrimSpace(stderr.String()))
		}
		diffText = string(out)
		source = strings.TrimSpace("git " + sub + " " + strings.Join(args, " "))
	}

	files := ParseDiff(diffText)
	if pagerMode && !reviewable(files) {
		// --stat, --word-diff, git log output...: show it the usual way
		return runFallbackPager(rawInput)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No changes to review.")
		return nil
	}
	files = SortTestsLast(files)

	// scope viewed marks to the repository
	root := ""
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		root = strings.TrimSpace(string(out))
	}
	for _, f := range files {
		sum := sha256.Sum256([]byte(root + "\x00" + f.Hash))
		f.Hash = hex.EncodeToString(sum[:])
	}

	// The UI talks to the terminal directly, so stdin/stdout can be pipes.
	in, out := os.Stdin, os.Stdout
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("cannot open terminal: %w", err)
		}
		defer tty.Close()
		if !isTTY(os.Stdin) {
			in = tty
		}
		if !isTTY(os.Stdout) {
			out = tty
		}
	}
	renderer := lipgloss.NewRenderer(out)
	if renderer.ColorProfile() < termenv.ANSI256 || renderer.ColorProfile() == termenv.Ascii {
		renderer.SetColorProfile(termenv.ANSI256)
	}
	if dark, ok := tmuxTheme(); ok {
		renderer.SetHasDarkBackground(dark)
	} else {
		// query now, before the UI starts reading terminal input
		renderer.SetHasDarkBackground(renderer.HasDarkBackground())
	}
	lipgloss.SetDefaultRenderer(renderer)

	m := newModel(source, files, OpenViewedStore())
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(),
		tea.WithInput(in), tea.WithOutput(out))

	go highlightAll(deltaCommand(), files, func(msg highlightMsg) { p.Send(msg) })

	if _, err := p.Run(); err != nil {
		return err
	}

	if len(m.comments) == 0 {
		return nil
	}
	if !m.printOutput {
		fmt.Fprintf(os.Stderr, "Discarded %d comment(s).\n", len(m.comments))
		return nil
	}
	review := FormatComments(source, files, m.comments)
	fmt.Print(review)
	if tool, err := CopyToClipboard(review, out); err != nil {
		fmt.Fprintf(os.Stderr, "\ndelta-review: could not copy to clipboard: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "\nCopied %d comment(s) to the clipboard (%s).\n", len(m.comments), tool)
	}
	return nil
}

// reviewable reports whether piped input is a diff we can show line by line.
func reviewable(files []*FileDiff) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if f.Malformed {
			return false
		}
	}
	return true
}

// runFallbackPager shows input we can't review with git's regular pager
// (core.pager / $GIT_PAGER / $PAGER), never with ourselves.
func runFallbackPager(input []byte) error {
	pager := ""
	if out, err := exec.Command("git", "var", "GIT_PAGER").Output(); err == nil {
		pager = strings.TrimSpace(string(out))
	}
	if pager == "" || strings.Contains(pager, "delta-review") {
		pager = "less -R"
	}
	if pager == "cat" {
		_, err := os.Stdout.Write(input)
		return err
	}
	cmd := exec.Command("sh", "-c", pager)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// gitParentCommand returns the command line of the git process that started
// us as its pager (e.g. "git diff --cached"), or "" if there is none. Linux only.
func gitParentCommand() string {
	pid := os.Getppid()
	for depth := 0; depth < 4 && pid > 1; depth++ {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			return ""
		}
		argv := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if len(argv) > 0 && filepath.Base(argv[0]) == "git" {
			// drop global options such as -c key=value, keep the subcommand and its args
			out := []string{"git"}
			for i := 1; i < len(argv); i++ {
				switch a := argv[i]; {
				case a == "-c" || a == "-C" || a == "--git-dir" || a == "--work-tree" || a == "--namespace":
					i++
				case len(out) == 1 && strings.HasPrefix(a, "-"):
				default:
					out = append(out, a)
				}
			}
			return strings.Join(out, " ")
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return ""
		}
		// fields after the parenthesised command name: state ppid ...
		rest := string(stat[strings.LastIndexByte(string(stat), ')')+1:])
		var state string
		if _, err := fmt.Sscan(rest, &state, &pid); err != nil {
			return ""
		}
	}
	return ""
}

// tmuxTheme reads the @theme option used by the delta-themed wrapper, so the
// UI colours follow the same light/dark choice as delta.
func tmuxTheme() (dark bool, ok bool) {
	if os.Getenv("TMUX") == "" {
		return false, false
	}
	out, err := exec.Command("tmux", "show", "-gqv", "@theme").Output()
	if err != nil {
		return false, false
	}
	switch strings.TrimSpace(string(out)) {
	case "dark":
		return true, true
	case "light":
		return false, true
	}
	return false, false
}
