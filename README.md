# delta-review

A terminal UI for reviewing a git diff, highlighted by [delta](https://github.com/dandavison/delta).
Leave comments on lines or ranges; on `q` they are printed as Markdown and copied to the
clipboard, ready to paste into a Claude Code session.

```sh
sudo ./install.sh                 # builds and installs /usr/local/bin/delta-review

delta-review                      # git diff
delta-review --cached             # any git diff arguments are passed through
delta-review main...HEAD
gh pr diff 123 | delta-review     # or read a diff from stdin
```

### As git's pager

```ini
[pager]
	diff = $(command -v delta-review || command -v delta-themed || echo delta)
	show = $(command -v delta-review || command -v delta-themed || echo delta)
	stash = $(command -v delta-review || command -v delta-themed || echo delta)
```

`git diff ...` (including `--cached`), `git show` and `git stash show -p` then open the review UI;
the commit message in `git show` output is skipped. Input that isn't a plain diff (`--stat`,
`--word-diff`, `--name-only`, `git stash list`, the messages of `git stash`/`git stash pop`) is
handed to git's regular pager (`core.pager`). Bypass once with `git -c pager.diff=delta diff`.

`pager.log` is best left unset: the review UI reads all of its input before showing anything,
so a plain `git log` would wait for the whole history.

## Features

- Syntax highlighting by your own delta config (git's `core.pager` if it is delta or a wrapper
  such as `delta-themed`; override with `DELTA_REVIEW_DELTA`). Falls back to plain red/green
  per file if delta is missing or its output can't be mapped onto the diff.
- Sticky file header while scrolling; mouse wheel scrolling; click and drag to select lines.
- `v` marks a file as viewed: it collapses and the cursor jumps to the next file. The mark is
  stored as a hash of the file's diff in `$XDG_STATE_HOME/delta-review/viewed.json`
  (default `~/.local/state`), so the file stays collapsed until its diff changes.
- Files whose path contains the word "test" (`foo_test.go`, `tests/`, `FooTest.java`, but not
  `latest.py`) are listed last.
- File list (`e`, hidden by default): a sidebar on wide terminals, a full-screen page on narrow
  ones (under 80 columns, e.g. a phone). Word wrap is on by default (`w`); line numbers are
  hidden by default (`#`). Status bar with key hints and a viewed progress bar.
- Comments live in memory only. On `q` they are printed and copied to the clipboard via
  `wl-copy`, `xclip`, `xsel` or `pbcopy` (override with `DELTA_REVIEW_CLIPBOARD`), falling back
  to the OSC 52 escape sequence.

## Keys

| Key | Action |
| --- | --- |
| `j`/`k`, arrows, wheel | move / scroll |
| `ctrl+d`/`ctrl+u`, `space`/`b`, `g`/`G` | half page, page, top/bottom |
| `n`/`p` (`]`/`[`), `}`/`{` | next/previous file, hunk |
| `V`, click+drag, shift+click | select a line range |
| `c` / `enter` | comment on line or selection (edits an existing comment) |
| `d d` | delete the comment under the cursor |
| `v` | toggle viewed |
| `o`/`z`, `Z` | collapse/expand file, all files |
| `w` / `#` / `e` / `?` | word wrap / line numbers / file list / help |
| `h`/`l` | horizontal scroll when wrap is off |
| `q` | quit, print comments and copy them to the clipboard |
| `ctrl+c` | quit without printing (asks again if there are comments) |

In the comment editor: `enter` saves, `alt+enter` (or `ctrl+j`) inserts a newline, `esc` cancels.
