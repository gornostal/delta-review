# delta-review — how it works

A terminal UI for reviewing a git diff before handing feedback to a coding agent. The user
scrolls the diff (highlighted by their own delta setup), marks files as viewed, leaves comments
on lines or line ranges, and on quit gets those comments as Markdown on stdout and in the
clipboard, ready to paste into a Claude Code session.

Written in Go on the Bubble Tea / Lip Gloss stack. Single binary, no runtime dependencies besides
git, and optionally delta and a clipboard tool.

## Ways it is started

1. **Directly**: `delta-review [git diff args]`. It runs `git diff` itself with colour, external
   diff tools and path quoting turned off and the standard `a/` `b/` prefixes forced.
   `delta-review show [args]` does the same with `git show`.
2. **Reading stdin**: `some-command | delta-review`, used for diffs from other tools.
3. **As git's pager** (`pager.diff`, `pager.show`). This is the stdin case with extra behaviour:
   - git colours output for pagers, so ANSI codes are stripped before parsing;
   - the user's prefix settings (mnemonic `i/` `w/`, or none) are detected from the headers;
   - the git command line is read from the parent process to label the review;
   - empty input exits silently;
   - input that is not a plain line-by-line diff (`--stat`, `--word-diff`, anything with
     malformed hunks) is passed unchanged to git's regular pager (`core.pager`), never to
     itself.

When stdin or stdout is not a terminal, the UI reads from and draws on `/dev/tty`, so both can
be pipes.

## Pipeline

1. **Parse** the unified diff into files → hunks → lines. Each line keeps its kind
   (context/added/removed/"no newline" marker) and its old and new line numbers. Paths are
   resolved after the whole header is read: rename/copy lines win, then `---`/`+++`, then the
   `diff --git` line. Renames, mode changes, binary files and empty files get a short note
   instead of hunks. Hunk line counts are checked; a mismatch marks the file malformed.
   Text outside files (commit headers and messages from `git show`, also between commits) is
   skipped: a line that isn't a header line ends the current file, so it never reaches delta
   or the fingerprint. The same path can appear more than once when several commits are shown.
2. **Order**: files whose path has a word starting with "test" (split on non-letters and
   camelCase, so `latest.py` doesn't match) move to the end. Otherwise the diff order is kept.
3. **Viewed state**: each file gets a fingerprint, a hash of its paths, status, notes and hunk
   text (not blob ids), salted with the repo root. Fingerprints marked as viewed are stored in
   `$XDG_STATE_HOME/delta-review/viewed.json` (default `~/.local/state`) with a timestamp. Files
   whose fingerprint is stored start collapsed. Any change to a file's diff gives it a new
   fingerprint, so it shows up again. Entries older than 90 days are dropped. The file is
   re-read before each write so parallel sessions don't overwrite each other.
4. **Highlight**: the UI starts at once with plain red/green colouring. Background workers then
   run delta once per file, in display order, and swap in the result. The delta command is
   `$DELTA_REVIEW_DELTA`, else git's pager if it is delta or a delta wrapper (but never
   delta-review), else `delta`.

### How delta output is mapped back to lines

delta's file and hunk headers are turned off, and it is told to fill line backgrounds with the
"erase to end of line" escape. With those settings delta prints **one blank line per hunk
followed by exactly one line per diff line**, so its output lines up with the parsed lines by
position. The mapping is checked: line count plus a blank at every hunk boundary. If the check
fails (for example the user's config enables side-by-side or line numbers), that file keeps the
plain colours and the status bar counts it.

The fill escape is stripped from each line, and the background colour active before it is
saved. The UI then pads every row (including wrapped continuation rows) to the pane width in
that colour. This makes delta's output independent of width, so resizing, wrapping and toggling
the sidebar never re-run delta. delta's line numbers and +/- markers aren't used; the UI draws
its own gutter (cursor/selection bar, comment marker, old and new numbers, sign).

## UI model

- **Items**: a fixed, flat list built once from the files: file header, notes, hunk headers and
  diff lines. Only file headers and diff lines (not "no newline" markers) can hold the cursor.
- **Rows**: screen rows rebuilt from the items whenever layout inputs change (collapse state,
  wrap, width, sidebar, comments):
  - collapsed files contribute only their header;
  - with wrap on, a long line becomes several rows;
  - each comment adds rows under the last line of its range.

  The view tries to keep the same item at the top across rebuilds.
- **Sticky header**: the top row of the diff pane always shows the header of the file being
  read. If the first visible row isn't a file header, it is drawn over by that file's header.
  Scrolling and cursor-visibility logic treat that row as covered.
- **Cursor and scrolling**: the keyboard moves the cursor and the view follows. The mouse wheel
  moves the view and the cursor is pulled back inside it.
- **Selection**: an anchor (set with `V`, a drag, or shift+click) plus the cursor. It never
  spans files: if the cursor leaves the file, the range is clipped to the file's edge.
- **Comments**: kept in memory only and never saved to disk. Each one holds a file, an
  inclusive line range and text. `c` on a line that already has a comment edits it; saving
  empty text deletes it.
- **Viewed**: `v` saves the fingerprint, collapses the file and jumps to the next file.
  Pressing it again removes the mark.
- **Layout**: optional file list (hidden by default; follows the current file, separates
  tests), diff pane, comment editor above the status bar while typing, and a status bar with
  key hints or a message plus comment count, a "nowrap" flag and a viewed progress bar. Word
  wrap is on and line numbers are off by default (`#` shows them; the gutter shrinks to
  cursor bar, comment marker and sign).
- **Narrow screens** (under 80 columns, e.g. a phone): the file list is a full-screen page
  instead of a sidebar, with its own selection (`fileSel`); picking a file closes it and jumps
  there. The file header, status hints and progress bar are shortened.
- **Input details**: several keys that arrive in one read are handled one at a time. Mouse
  reporting uses cell-motion mode, so wheel, click and drag work (native terminal selection
  needs shift).
- **Colours**: ANSI-256 palette colours that adapt to light/dark. Inside tmux, the `@theme`
  option (the one the user's delta wrapper reads) decides; elsewhere the terminal background is
  queried before the UI starts reading input.

## Output on quit

- `q`: comments are sorted by file order and line, then printed as Markdown. Each one gets a
  `path:line` (or `path:start-end`) heading, a fenced `diff` snippet of the commented lines
  (long ranges are trimmed in the middle) and the comment text.
  - Line numbers are new-file numbers; a range of only removed lines uses old numbers and says
    so.
  - The same text goes to the clipboard: `$DELTA_REVIEW_CLIPBOARD`, else `pbcopy` / `wl-copy`
    (also used when only the `wayland-0` socket exists, as in tmux servers started without
    `WAYLAND_DISPLAY`) / `xclip` / `xsel`, else an OSC 52 escape (wrapped for tmux
    passthrough).
- `ctrl+c`: quits without output. If there are comments it asks for a second press first.

## Development

```sh
go test ./...          # parser, path/prefix handling, delta mapping (needs delta), output format
go vet ./...
go install .           # installs to ~/go/bin/delta-review (git's pager.diff points at it)
```

To check behaviour end to end, run it in a detached tmux session (`tmux new-session -d -x 120
-y 30 ...`), drive it with `tmux send-keys` (raw SGR sequences such as `\e[<65;X;YM` work for
mouse wheel/clicks), and read the screen with `tmux capture-pane -p` (add `-e` to see colours).
Use a scratch repo and set `XDG_STATE_HOME` to a temp dir so the real viewed state isn't
touched.

Invariants worth keeping:

- The one-blank-per-hunk delta mapping. Anything that changes the diff text sent to delta must
  keep the check, and fall back to plain colours rather than show misaligned lines.
- Pager mode must never page through itself (recursion through `core.pager` or the delta
  command lookup).
- Fingerprints must not depend on blob ids, prefixes or colour, so the same change hashes the
  same whether it came from the direct, stdin or pager path.
