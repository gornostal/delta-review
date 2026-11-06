package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	colDim       = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}
	colFg        = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	colHeaderBg  = lipgloss.AdaptiveColor{Light: "254", Dark: "236"}
	colSel       = lipgloss.AdaptiveColor{Light: "153", Dark: "24"}
	colCursor    = lipgloss.AdaptiveColor{Light: "166", Dark: "214"}
	colComment   = lipgloss.AdaptiveColor{Light: "130", Dark: "221"}
	colCommentBg = lipgloss.AdaptiveColor{Light: "230", Dark: "235"}
	colGreen     = lipgloss.AdaptiveColor{Light: "28", Dark: "71"}
	colRed       = lipgloss.AdaptiveColor{Light: "160", Dark: "167"}
	colHunk      = lipgloss.AdaptiveColor{Light: "25", Dark: "74"}
	colStatusBg  = lipgloss.AdaptiveColor{Light: "252", Dark: "235"}
)

const reset = "\x1b[0m"

func st() lipgloss.Style { return lipgloss.NewStyle() }

// fit truncates or pads s to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		s = ansi.Truncate(s, w, "…")
		sw = ansi.StringWidth(s)
	}
	return s + strings.Repeat(" ", max(0, w-sw))
}

// truncLeft shortens a path from the left: "…/pkg/foo.go".
func truncLeft(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return "…" + ansi.TruncateLeft(s, ansi.StringWidth(s)-w+1, "")
}

func (m *model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	h := m.viewH()
	sw := m.sidebarW()
	var side, diff []string
	switch {
	case m.fileListFull() && m.mode != modeHelp:
		diff = m.renderSidebar(m.width, h)
	case sw > 0:
		side = m.renderSidebar(sw, h)
		diff = m.renderDiff(h)
	default:
		diff = m.renderDiff(h)
	}
	sep := st().Foreground(colDim).Render("│")

	out := make([]string, 0, m.height)
	for y := 0; y < h; y++ {
		if sw > 0 {
			out = append(out, side[y]+sep+diff[y])
		} else {
			out = append(out, diff[y])
		}
	}
	if m.mode == modeComment {
		out = append(out, m.renderEditor()...)
	}
	out = append(out, m.renderStatus())
	return strings.Join(out, "\n")
}

// ---------- diff area ----------

func (m *model) renderDiff(h int) []string {
	dw := m.diffW()
	lines := make([]string, h)
	if m.mode == modeHelp {
		help := helpLines()
		for y := range lines {
			if y < len(help) {
				lines[y] = fit(help[y], dw)
			} else {
				lines[y] = strings.Repeat(" ", dw)
			}
		}
		return lines
	}
	for y := 0; y < h; y++ {
		r := m.offset + y
		switch {
		case r >= len(m.rows):
			lines[y] = strings.Repeat(" ", dw)
		case y == 0 && !m.isHeaderRow(r):
			lines[y] = m.renderFileHeader(m.items[m.rows[r].item].file, true)
		default:
			lines[y] = m.renderRow(m.rows[r])
		}
	}
	return lines
}

func (m *model) renderFileHeader(fi int, sticky bool) string {
	f := m.files[fi]
	dw := m.diffW()
	bg := st().Background(colHeaderBg)
	if sticky {
		bg = bg.Underline(false)
	}
	s := func(style lipgloss.Style, text string) string { return style.Inherit(bg).Render(text) }

	bar := s(st(), " ")
	if !sticky && m.cursor == m.fileItem[fi] {
		bar = s(st().Foreground(colCursor), "▌")
	}
	arrow := "▾"
	if m.collapsed[fi] {
		arrow = "▸"
	}

	// right side
	var right []string
	nComments := 0
	for _, c := range m.comments {
		if c.File == fi {
			nComments++
		}
	}
	if nComments > 0 {
		right = append(right, s(st().Foreground(colComment), fmt.Sprintf("● %d", nComments)))
	}
	if f.Added > 0 || f.Removed > 0 {
		right = append(right, s(st().Foreground(colGreen), fmt.Sprintf("+%d", f.Added))+s(st(), " ")+
			s(st().Foreground(colRed), fmt.Sprintf("-%d", f.Removed)))
	}
	viewedLbl := " Viewed"
	if m.narrow() {
		viewedLbl = ""
	}
	if m.viewed[fi] {
		right = append(right, s(st().Foreground(colGreen).Bold(true), "✓"+viewedLbl))
	} else {
		right = append(right, s(st().Foreground(colDim), "☐"+viewedLbl))
	}
	rightStr := strings.Join(right, s(st(), "  ")) + s(st(), " ")
	rw := ansi.StringWidth(rightStr)

	label := ""
	switch {
	case f.Status == "added":
		label = "new"
	case f.Status == "deleted":
		label = "deleted"
	case f.Status == "renamed":
		label = "renamed"
	case f.Status == "copied":
		label = "copied"
	}
	if f.Binary {
		label = strings.TrimSpace(label + " binary")
	}
	if f.IsTest {
		label = strings.TrimSpace(label + " test")
	}
	labelW := 0
	if label != "" {
		labelW = len(label) + 2
	}
	pathW := max(5, dw-4-rw-labelW-2)
	path := truncLeft(f.DisplayPath(), pathW)

	left := bar + s(st().Foreground(colDim), " "+arrow+" ") + s(st().Bold(true).Foreground(colFg), path)
	if label != "" {
		left += s(st(), "  ") + s(st().Foreground(colHunk), label)
	}
	gap := dw - ansi.StringWidth(left) - rw
	if gap < 1 {
		return fit(left, dw)
	}
	return left + s(st(), strings.Repeat(" ", gap)) + rightStr
}

func (m *model) renderRow(rw row) string {
	it := m.items[rw.item]
	cw := m.contentW()
	gw := m.gutterW()
	if rw.comment >= 0 {
		return m.renderCommentRow(rw, gw, cw)
	}
	switch it.kind {
	case itemFile:
		return m.renderFileHeader(it.file, false)
	case itemHunk:
		f := m.files[it.file]
		dots := "  ⋯"
		if m.showNums {
			dots = "  " + strings.Repeat(" ", 2*m.numW-2) + "⋯"
		}
		gut := st().Foreground(colDim).Render(fit(dots, gw))
		text := f.Hunks[it.idx].Header
		return gut + st().Foreground(colHunk).Render(fit(text, cw))
	case itemNote:
		note := m.files[it.file].Notes[it.idx]
		return strings.Repeat(" ", gw) + st().Foreground(colDim).Italic(true).Render(fit(note, cw))
	}
	return m.renderLine(rw, it, gw, cw)
}

func (m *model) renderLine(rw row, it item, gw, cw int) string {
	f := m.files[it.file]
	l := &f.Lines[it.idx]
	isCursor := rw.item == m.cursor && m.mode != modeHelp
	inSel := m.inSelection(it.file, it.idx)
	if m.mode == modeComment && it.file == m.editFile && it.idx >= m.editStart && it.idx <= m.editEnd {
		inSel = true
	}

	g := st()
	if isCursor || inSel {
		g = g.Background(colSel)
	}
	bar := g.Render(" ")
	if isCursor {
		bar = g.Foreground(colCursor).Render("▌")
	} else if inSel {
		bar = g.Foreground(colFg).Render("▌")
	}
	mark := g.Render(" ")
	if m.commentedSet[[2]int{it.file, it.idx}] {
		mark = g.Foreground(colComment).Render("●")
	}
	num := func(n int) string {
		if n == 0 || rw.seg > 0 {
			return strings.Repeat(" ", m.numW)
		}
		return fmt.Sprintf("%*d", m.numW, n)
	}
	oldS, newS, sign := g.Foreground(colDim), g.Foreground(colDim), g
	signCh := " "
	switch l.Kind {
	case Added:
		newS = g.Foreground(colGreen)
		sign = g.Foreground(colGreen)
		signCh = "+"
	case Removed:
		oldS = g.Foreground(colRed)
		sign = g.Foreground(colRed)
		signCh = "-"
	}
	if rw.seg > 0 {
		signCh = " "
	}
	gutter := bar + mark
	if m.showNums {
		gutter += oldS.Render(num(l.OldNo)) + g.Render(" ") + newS.Render(num(l.NewNo)) + g.Render(" ")
	}
	gutter += sign.Render(signCh) + g.Render(" ")
	_ = gw

	var seg string
	if m.wrap {
		seg = ansi.Cut(l.Text, rw.seg*cw, (rw.seg+1)*cw)
	} else {
		if l.Width-m.hoff > cw {
			seg = ansi.Cut(l.Text, m.hoff, m.hoff+cw-1) + reset + st().Foreground(colDim).Render("»")
		} else {
			seg = ansi.Cut(l.Text, m.hoff, m.hoff+cw)
		}
	}
	w := ansi.StringWidth(seg)
	out := gutter + seg + reset
	if w < cw {
		out += l.Fill + strings.Repeat(" ", cw-w) + reset
	}
	return out
}

func (m *model) renderCommentRow(rw row, gw, cw int) string {
	c := m.comments[rw.comment]
	bg := st().Background(colCommentBg)
	bar := bg.Foreground(colComment)
	var content string
	if rw.seg == 0 {
		lbl := "Comment · " + m.files[c.File].Path() + ":" + LineLabel(m.files[c.File], c.Start, c.End)
		if m.mode == modeComment && m.editing == rw.comment {
			lbl += " (editing)"
		}
		content = bar.Render(" ╭─ ") + bg.Foreground(colComment).Bold(true).Render(fit(lbl, cw-4))
	} else {
		content = bar.Render(" │  ") + bg.Foreground(colFg).Render(fit(m.commentText[rw.comment][rw.seg], cw-4))
	}
	return strings.Repeat(" ", gw) + content
}

// ---------- sidebar ----------

func (m *model) renderSidebar(sw, h int) []string {
	type srow struct {
		text string
		file int
	}
	var rows []srow
	title := st().Bold(true).Render(" Files")
	count := st().Foreground(colDim).Render(fmt.Sprintf("%d/%d viewed ", m.viewedCount(), len(m.files)))
	rows = append(rows, srow{title + strings.Repeat(" ", max(1, sw-ansi.StringWidth(title)-ansi.StringWidth(count))) + count, -1})

	curFile := m.curFile()
	if m.fileListFull() {
		curFile = m.fileSel
	}
	commentsPer := map[int]int{}
	for _, c := range m.comments {
		commentsPer[c.File]++
	}
	curRow := 0
	for i, f := range m.files {
		if f.IsTest && (i == 0 || !m.files[i-1].IsTest) && i > 0 {
			rows = append(rows, srow{st().Foreground(colDim).Render(fit(" ── tests "+strings.Repeat("─", max(0, sw-10)), sw)), -1})
		}
		isCur := i == curFile
		base := st()
		if isCur {
			base = base.Background(colHeaderBg)
		}
		s := func(style lipgloss.Style, t string) string { return style.Inherit(base).Render(t) }

		bar := s(st(), " ")
		if isCur {
			bar = s(st().Foreground(colCursor), "▌")
		}
		icon := s(st(), "  ")
		if m.viewed[i] {
			icon = s(st().Foreground(colGreen), "✓ ")
		}
		right := ""
		if n := commentsPer[i]; n > 0 {
			right += s(st().Foreground(colComment), fmt.Sprintf("●%d ", n))
		}
		if f.Added > 0 || f.Removed > 0 {
			right += s(st().Foreground(colGreen), fmt.Sprintf("+%d", f.Added)) + s(st(), " ") +
				s(st().Foreground(colRed), fmt.Sprintf("-%d", f.Removed))
		}
		right += s(st(), " ")
		avail := sw - 3 - ansi.StringWidth(right) - 1
		p := truncLeft(f.Path(), max(4, avail))
		dir, name := "", p
		if j := strings.LastIndex(p, "/"); j >= 0 {
			dir, name = p[:j+1], p[j+1:]
		}
		nameStyle := st().Foreground(colFg)
		if m.viewed[i] {
			nameStyle = st().Foreground(colDim)
		}
		if isCur {
			nameStyle = nameStyle.Bold(true)
		}
		left := bar + icon + s(st().Foreground(colDim), dir) + s(nameStyle, name)
		gap := sw - ansi.StringWidth(left) - ansi.StringWidth(right)
		line := left + s(st(), strings.Repeat(" ", max(0, gap))) + right
		if gap < 0 {
			line = fit(left, sw)
		}
		if isCur {
			curRow = len(rows)
		}
		rows = append(rows, srow{line, i})
	}

	// follow the current file, but let the mouse wheel scroll freely otherwise
	if curFile != m.lastSideFile {
		m.lastSideFile = curFile
		if curRow < m.sideOff+1 {
			m.sideOff = max(0, curRow-1)
		} else if curRow >= m.sideOff+h {
			m.sideOff = curRow - h + 1
		}
	}
	m.sideOff = max(0, min(m.sideOff, len(rows)-h))

	m.sideRows = m.sideRows[:0]
	for _, r := range rows {
		m.sideRows = append(m.sideRows, r.file)
	}
	out := make([]string, h)
	for y := range out {
		r := m.sideOff + y
		if r < len(rows) {
			out[y] = fit(rows[r].text, sw)
		} else {
			out[y] = strings.Repeat(" ", sw)
		}
	}
	return out
}

// ---------- editor, status, help ----------

func (m *model) renderEditor() []string {
	f := m.files[m.editFile]
	verb := "New comment on "
	if m.editing >= 0 {
		verb = "Edit comment on "
	}
	title := st().Foreground(colComment).Bold(true).Render(" ✎ "+verb+f.Path()+":"+LineLabel(f, m.editStart, m.editEnd)) +
		st().Foreground(colDim).Render("   enter save · alt+enter newline · esc cancel")
	if m.editing >= 0 {
		title += st().Foreground(colDim).Render(" · empty text deletes")
	}
	lines := []string{fit(title, m.width)}
	for _, l := range strings.Split(m.ta.View(), "\n") {
		lines = append(lines, fit(l, m.width))
	}
	for len(lines) < editorHeight {
		lines = append(lines, strings.Repeat(" ", m.width))
	}
	return lines[:editorHeight]
}

func (m *model) renderStatus() string {
	bg := st().Background(colStatusBg)
	s := func(style lipgloss.Style, t string) string { return style.Inherit(bg).Render(t) }

	// right: comments, wrap, progress
	var right []string
	if m.deltaErrors > 0 {
		right = append(right, s(st().Foreground(colRed), fmt.Sprintf("plain colours: %d file(s)", m.deltaErrors)))
	}
	if n := len(m.comments); n > 0 {
		right = append(right, s(st().Foreground(colComment), fmt.Sprintf("● %d comment%s", n, plural(n))))
	}
	if !m.wrap {
		right = append(right, s(st().Foreground(colDim), "nowrap"))
	}
	if m.anchor >= 0 {
		_, lo, hi, _ := m.selection()
		right = append(right, s(st().Foreground(colCursor), fmt.Sprintf("%d lines selected", hi-lo+1)))
	}
	total, viewed := len(m.files), m.viewedCount()
	barW := 16
	if m.narrow() {
		barW = 6
	}
	filled := 0
	if total > 0 {
		filled = (barW*viewed + total/2) / total
	}
	bar := s(st().Foreground(colGreen), strings.Repeat("█", filled)) +
		s(st().Foreground(colDim), strings.Repeat("░", barW-filled))
	viewedLbl := " viewed"
	if m.narrow() {
		viewedLbl = ""
	}
	right = append(right, bar+s(st(), fmt.Sprintf(" %d/%d%s ", viewed, total, viewedLbl)))
	rightStr := strings.Join(right, s(st(), "  "))
	rw := ansi.StringWidth(rightStr)

	var left string
	if m.message != "" {
		left = s(st().Foreground(colCursor).Bold(true), " "+m.message)
	} else {
		var hints [][2]string
		switch {
		case m.mode == modeComment:
			hints = [][2]string{{"enter", "save"}, {"alt+enter", "newline"}, {"esc", "cancel"}}
		case m.mode == modeHelp:
			hints = [][2]string{{"any key", "close help"}}
		case m.fileListFull():
			hints = [][2]string{{"j/k", "move"}, {"enter", "open"}, {"e", "close"}}
		case m.narrow():
			hints = [][2]string{{"c", "comment"}, {"v", "viewed"}, {"e", "files"}, {"?", "help"}}
		default:
			hints = [][2]string{{"j/k", "move"}, {"V", "select"}, {"c", "comment"}, {"v", "viewed"},
				{"n/p", "file"}, {"w", "wrap"}, {"e", "files"}, {"?", "help"}, {"q", "quit"}}
		}
		left = s(st(), " ")
		for i, h := range hints {
			if i > 0 {
				left += s(st(), "  ")
			}
			left += s(st().Bold(true).Foreground(colFg), h[0]) + s(st().Foreground(colDim), " "+h[1])
		}
	}
	avail := m.width - rw - 1
	if avail < 0 {
		return fit(rightStr, m.width)
	}
	if ansi.StringWidth(left) > avail {
		left = ansi.Truncate(left, avail, "…")
	}
	gap := m.width - ansi.StringWidth(left) - rw
	return left + s(st(), strings.Repeat(" ", max(0, gap))) + rightStr
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func helpLines() []string {
	k := func(keys, desc string) string {
		return "   " + st().Bold(true).Render(fmt.Sprintf("%-18s", keys)) + st().Foreground(colDim).Render(desc)
	}
	h := func(t string) string { return " " + st().Bold(true).Foreground(colHunk).Render(t) }
	return []string{
		"",
		h("Navigation"),
		k("j/k  ↓/↑", "move cursor"),
		k("ctrl+d / ctrl+u", "half page down / up"),
		k("space / b", "page down / up"),
		k("g / G", "top / bottom"),
		k("n / p   ] / [", "next / previous file"),
		k("} / {", "next / previous hunk"),
		k("h / l   ← / →", "scroll horizontally (wrap off), 0 resets"),
		k("mouse wheel", "scroll"),
		"",
		h("Review"),
		k("V", "start / stop a line range selection"),
		k("click, drag", "move cursor / select a range (shift+click extends)"),
		k("c / enter", "comment on line or selection (edits an existing one)"),
		k("d d", "delete comment under cursor"),
		k("v", "toggle file viewed (collapses it, jumps to next file)"),
		k("o / z / enter", "collapse / expand file (enter on file header)"),
		k("Z", "collapse / expand all files"),
		"",
		h("View"),
		k("w", "toggle word wrap"),
		k("#", "toggle line numbers"),
		k("e", "toggle file list (full screen on narrow terminals)"),
		k("?", "toggle this help"),
		"",
		h("Quit"),
		k("q", "quit, print comments and copy them to the clipboard"),
		k("ctrl+c", "quit without printing"),
		"",
		"   " + st().Foreground(colDim).Render("Viewed state: "+stateDir()+"/viewed.json"),
	}
}
