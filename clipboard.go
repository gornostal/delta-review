package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CopyToClipboard puts text on the system clipboard and returns the name of
// the mechanism used. It tries $DELTA_REVIEW_CLIPBOARD, then the platform's
// clipboard tools, and finally the OSC 52 escape sequence written to term
// (which only works if the terminal, and tmux if any, allow it).
func CopyToClipboard(text string, term io.Writer) (string, error) {
	var candidates [][]string
	if c := strings.TrimSpace(os.Getenv("DELTA_REVIEW_CLIPBOARD")); c != "" {
		candidates = append(candidates, []string{"sh", "-c", c})
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, []string{"pbcopy"})
	}
	if hasWayland() {
		candidates = append(candidates, []string{"wl-copy"})
	}
	if os.Getenv("DISPLAY") != "" {
		candidates = append(candidates,
			[]string{"xclip", "-selection", "clipboard", "-in"},
			[]string{"xsel", "--clipboard", "--input"})
	}

	var errs []string
	for _, argv := range candidates {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		if err := runClipboardCmd(argv, text); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", argv[0], err))
			continue
		}
		if argv[0] == "sh" {
			return "DELTA_REVIEW_CLIPBOARD", nil
		}
		return argv[0], nil
	}

	if term != nil {
		seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
		if os.Getenv("TMUX") != "" {
			// tmux passthrough, in case set-clipboard is off/external
			seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
		}
		if _, err := io.WriteString(term, seq); err == nil {
			return "OSC 52 terminal escape", nil
		}
	}
	if len(errs) > 0 {
		return "", fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return "", fmt.Errorf("no clipboard tool found (install wl-clipboard, xclip or xsel)")
}

// hasWayland is true inside a Wayland session, including tmux servers that
// were started without WAYLAND_DISPLAY (wl-copy then uses wayland-0).
func hasWayland() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "wayland-0")); err == nil {
			return true
		}
	}
	return false
}

func runClipboardCmd(argv []string, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	// no stdout/stderr pipes: wl-copy and xclip fork a process that keeps
	// serving the selection, and an open pipe would make us wait for it
	return cmd.Run()
}
