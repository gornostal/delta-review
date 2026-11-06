package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type itemKind int

const (
	itemFile itemKind = iota
	itemNote
	itemHunk
	itemLine
)

type item struct {
	kind itemKind
	file int
	idx  int // note, hunk or line index within the file
}

// row is one screen row of the diff area.
type row struct {
	item    int // index into model.items; for comment rows, the line the comment is attached to
	seg     int // wrap segment, or line of the comment block
	comment int // comment index, or -1
}

type mode int

const (
	modeNormal mode = iota
	modeComment
	modeHelp
)

const editorHeight = 4

type model struct {
	source string
	files  []*FileDiff
	store  *ViewedStore

	viewed    []bool
	collapsed []bool
	items     []item
	fileItem  []int   // file -> item index of its header
	lineItem  [][]int // file -> line -> item index
	rows      []row
	itemRow   []int // item -> first row, -1 when hidden

	comments     []Comment
	commentText  [][]string // per comment: wrapped text lines (computed in layout)
	commentedSet map[[2]int]bool

	width, height int
	offset        int
	hoff          int
	cursor        int // item index
	anchor        int // item index where the selection started, -1 if none
	dragging      bool
	numW          int

	wrap         bool
	showNums     bool
	showFiles    bool
	fileSel      int // highlighted file in the full-screen file list (narrow screens)
	sideOff      int
	sideRows     []int // sidebar row -> file index (-1 for non-file rows)
	lastSideFile int

	mode      mode
	editing   int // comment index being edited, -1 for a new one
	editFile  int
	editStart int
	editEnd   int
	ta        textarea.Model

	message       string
	pendingDelete bool
	pendingQuit   bool
	printOutput   bool
	deltaErrors   int
	firstDeltaErr string
}

func newModel(source string, files []*FileDiff, store *ViewedStore) *model {
	m := &model{
		source:       source,
		files:        files,
		store:        store,
		viewed:       make([]bool, len(files)),
		collapsed:    make([]bool, len(files)),
		fileItem:     make([]int, len(files)),
		lineItem:     make([][]int, len(files)),
		anchor:       -1,
		editing:      -1,
		lastSideFile: -1,
		wrap:         true,
	}
	maxNo := 0
	for fi, f := range files {
		m.viewed[fi] = store.IsViewed(f.Hash)
		m.collapsed[fi] = m.viewed[fi]
		m.fileItem[fi] = len(m.items)
		m.items = append(m.items, item{itemFile, fi, 0})
		for ni := range f.Notes {
			m.items = append(m.items, item{itemNote, fi, ni})
		}
		m.lineItem[fi] = make([]int, len(f.Lines))
		for hi, h := range f.Hunks {
			m.items = append(m.items, item{itemHunk, fi, hi})
			for li := h.Start; li < h.End; li++ {
				m.lineItem[fi][li] = len(m.items)
				m.items = append(m.items, item{itemLine, fi, li})
			}
		}
		for i := range f.Lines {
			l := &f.Lines[i]
			hl := PlainHighlight(l)
			l.Text, l.Fill, l.Width = hl.Text, hl.Fill, hl.Width
			maxNo = max(maxNo, l.OldNo, l.NewNo)
		}
	}
	m.numW = max(3, len(fmt.Sprint(maxNo)))

	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = "┃ "
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.SetHeight(editorHeight - 1)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colComment)
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	m.ta = ta

	// start on the first file that still needs review
	for fi := range files {
		if !m.viewed[fi] {
			m.cursor = m.fileItem[fi]
			break
		}
	}
	return m
}

func (m *model) Init() tea.Cmd { return nil }

// ---------- geometry ----------

// narrowW is the width below which the screen is treated as narrow (a phone):
// the file list takes the whole screen instead of being a sidebar.
const narrowW = 80

func (m *model) narrow() bool { return m.width < narrowW }

// fileListFull reports whether the file list is open as a full-screen page.
func (m *model) fileListFull() bool { return m.showFiles && m.narrow() }

func (m *model) sidebarW() int {
	if !m.showFiles || m.narrow() {
		return 0
	}
	longest := 0
	for _, f := range m.files {
		longest = max(longest, ansi.StringWidth(f.Path()))
	}
	return min(max(longest+14, 28), m.width*35/100)
}

func (m *model) diffX() int {
	if sw := m.sidebarW(); sw > 0 {
		return sw + 1
	}
	return 0
}

func (m *model) diffW() int   { return max(20, m.width-m.diffX()) }
func (m *model) gutterW() int {
	if m.showNums {
		return 2*m.numW + 6
	}
	return 4 // cursor bar, comment mark, sign, space
}
func (m *model) contentW() int {
	return max(10, m.diffW()-m.gutterW())
}

func (m *model) viewH() int {
	h := m.height - 1
	if m.mode == modeComment {
		h -= editorHeight
	}
	return max(1, h)
}

// ---------- layout ----------

func (m *model) layout() {
	// remember what is at the top so the view does not jump
	topItem, topSeg := -1, 0
	if m.offset < len(m.rows) {
		topItem, topSeg = m.rows[m.offset].item, m.rows[m.offset].seg
	}

	cw := m.contentW()
	ends := map[[2]int][]int{}
	m.commentedSet = map[[2]int]bool{}
	m.commentText = make([][]string, len(m.comments))
	for ci, c := range m.comments {
		k := [2]int{c.File, c.End}
		ends[k] = append(ends[k], ci)
		for l := c.Start; l <= c.End; l++ {
			m.commentedSet[[2]int{c.File, l}] = true
		}
		text := ansi.Wrap(strings.TrimSpace(c.Text), max(10, cw-3), "")
		m.commentText[ci] = append([]string{""}, strings.Split(text, "\n")...)
	}

	m.rows = m.rows[:0]
	if len(m.itemRow) != len(m.items) {
		m.itemRow = make([]int, len(m.items))
	}
	for ii, it := range m.items {
		if it.kind != itemFile && m.collapsed[it.file] {
			m.itemRow[ii] = -1
			continue
		}
		m.itemRow[ii] = len(m.rows)
		segs := 1
		if it.kind == itemLine && m.wrap {
			w := m.files[it.file].Lines[it.idx].Width
			segs = max(1, (w+cw-1)/cw)
		}
		for s := 0; s < segs; s++ {
			m.rows = append(m.rows, row{item: ii, seg: s, comment: -1})
		}
		if it.kind == itemLine {
			for _, ci := range ends[[2]int{it.file, it.idx}] {
				for k := range m.commentText[ci] {
					m.rows = append(m.rows, row{item: ii, seg: k, comment: ci})
				}
			}
		}
	}

	if m.itemRow[m.cursor] < 0 {
		m.cursor = m.fileItem[m.items[m.cursor].file]
	}
	if topItem >= 0 && m.itemRow[topItem] >= 0 {
		m.offset = m.itemRow[topItem] + topSeg
	}
	m.clampOffset()
}

func (m *model) clampOffset() {
	maxOff := max(0, len(m.rows)-m.viewH())
	m.offset = max(0, min(m.offset, maxOff))
}

func (m *model) isHeaderRow(r int) bool {
	return r >= 0 && r < len(m.rows) && m.rows[r].comment < 0 && m.items[m.rows[r].item].kind == itemFile
}

func (m *model) selectable(ii int) bool {
	it := m.items[ii]
	switch it.kind {
	case itemFile:
		return true
	case itemLine:
		return m.files[it.file].Lines[it.idx].Kind != NoNewline
	}
	return false
}

func (m *model) curFile() int { return m.items[m.cursor].file }

// usable rows: the first row is covered by the sticky header unless it is a header itself
func (m *model) visibleTop() int {
	if m.isHeaderRow(m.offset) {
		return m.offset
	}
	return m.offset + 1
}

func (m *model) visibleBottom() int {
	return min(len(m.rows)-1, m.offset+m.viewH()-1)
}

func (m *model) ensureVisible() {
	r0 := m.itemRow[m.cursor]
	if r0 < 0 {
		return
	}
	r1 := r0
	for r1+1 < len(m.rows) && m.rows[r1+1].item == m.cursor && m.rows[r1+1].comment < 0 {
		r1++
	}
	if r1 > m.offset+m.viewH()-1 {
		m.offset = r1 - m.viewH() + 1
	}
	if r0 <= m.offset {
		if m.isHeaderRow(r0) {
			m.offset = r0
		} else {
			m.offset = r0 - 1
		}
	}
	m.clampOffset()
}

func (m *model) selectableAtOrAfter(r int) int {
	for ; r < len(m.rows); r++ {
		rw := m.rows[r]
		if rw.comment < 0 && rw.seg == 0 && m.selectable(rw.item) {
			return rw.item
		}
	}
	return -1
}

func (m *model) selectableAtOrBefore(r int) int {
	for r = min(r, len(m.rows)-1); r >= 0; r-- {
		rw := m.rows[r]
		if rw.comment < 0 && m.selectable(rw.item) {
			return rw.item
		}
	}
	return -1
}

// keep the cursor inside the viewport after scrolling
func (m *model) cursorIntoView() {
	r0 := m.itemRow[m.cursor]
	top, bottom := m.visibleTop(), m.visibleBottom()
	if r0 < top {
		if it := m.selectableAtOrAfter(top); it >= 0 {
			m.cursor = it
		}
	} else if r0 > bottom {
		if it := m.selectableAtOrBefore(bottom); it >= 0 {
			m.cursor = it
		}
	}
}

func (m *model) scroll(n int) {
	m.offset += n
	m.clampOffset()
	m.cursorIntoView()
}

func (m *model) moveCursor(n int) {
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	i := m.cursor
	for n > 0 {
		i += step
		if i < 0 || i >= len(m.items) {
			break
		}
		if m.itemRow[i] >= 0 && m.selectable(i) {
			m.cursor = i
			n--
		}
	}
	m.ensureVisible()
}

func (m *model) moveRows(n int) {
	r := m.itemRow[m.cursor] + n
	m.offset += n
	m.clampOffset()
	var it int
	if n > 0 {
		it = m.selectableAtOrAfter(min(r, len(m.rows)-1))
		if it < 0 {
			it = m.selectableAtOrBefore(len(m.rows) - 1)
		}
	} else {
		it = m.selectableAtOrBefore(max(r, 0))
		if it < 0 {
			it = m.selectableAtOrAfter(0)
		}
	}
	if it >= 0 {
		m.cursor = it
	}
	m.ensureVisible()
}

func (m *model) jumpToFile(fi int) {
	m.cursor = m.fileItem[fi]
	m.offset = m.itemRow[m.cursor]
	m.clampOffset()
}

func (m *model) jumpHunk(dir int) {
	i := m.cursor
	for {
		i += dir
		if i < 0 || i >= len(m.items) {
			return
		}
		if m.itemRow[i] >= 0 && m.items[i].kind == itemHunk {
			// land on the first changed line of the hunk
			for j := i + 1; j < len(m.items) && m.items[j].kind == itemLine; j++ {
				if m.files[m.items[j].file].Lines[m.items[j].idx].Kind != Context && m.selectable(j) {
					m.cursor = j
					m.ensureVisible()
					return
				}
			}
			if j := i + 1; j < len(m.items) && m.selectable(j) {
				m.cursor = j
				m.ensureVisible()
			}
			return
		}
	}
}

// ---------- selection & comments ----------

// selection returns the file and inclusive line range currently selected.
func (m *model) selection() (file, lo, hi int, ok bool) {
	cur := m.items[m.cursor]
	if m.anchor < 0 {
		if cur.kind != itemLine {
			return 0, 0, 0, false
		}
		return cur.file, cur.idx, cur.idx, true
	}
	a := m.items[m.anchor]
	lo, hi = a.idx, a.idx
	switch {
	case cur.kind == itemLine && cur.file == a.file:
		lo, hi = min(a.idx, cur.idx), max(a.idx, cur.idx)
	case m.cursor > m.anchor:
		hi = len(m.files[a.file].Lines) - 1
	default:
		lo = 0
	}
	return a.file, lo, hi, true
}

func (m *model) inSelection(file, line int) bool {
	if m.anchor < 0 {
		return false
	}
	f, lo, hi, ok := m.selection()
	return ok && f == file && line >= lo && line <= hi
}

func (m *model) commentAtCursor() int {
	cur := m.items[m.cursor]
	if cur.kind != itemLine {
		return -1
	}
	for ci, c := range m.comments {
		if c.File == cur.file && cur.idx >= c.Start && cur.idx <= c.End {
			return ci
		}
	}
	return -1
}

func (m *model) startComment() tea.Cmd {
	file, lo, hi, ok := m.selection()
	if !ok {
		m.message = "Move the cursor to a diff line to comment"
		return nil
	}
	m.editing = -1
	if m.anchor >= 0 {
		for ci, c := range m.comments {
			if c.File == file && c.Start == lo && c.End == hi {
				m.editing = ci
			}
		}
	} else {
		m.editing = m.commentAtCursor()
	}
	m.ta.Reset()
	if m.editing >= 0 {
		c := m.comments[m.editing]
		file, lo, hi = c.File, c.Start, c.End
		m.ta.SetValue(c.Text)
	}
	m.editFile, m.editStart, m.editEnd = file, lo, hi
	m.mode = modeComment
	m.ta.SetWidth(m.width)
	m.ensureVisible()
	return m.ta.Focus()
}

func (m *model) saveComment() {
	text := strings.TrimSpace(m.ta.Value())
	switch {
	case m.editing >= 0 && text == "":
		m.comments = append(m.comments[:m.editing], m.comments[m.editing+1:]...)
		m.message = "Comment deleted"
	case m.editing >= 0:
		m.comments[m.editing].Text = text
		m.message = "Comment updated"
	case text != "":
		m.comments = append(m.comments, Comment{File: m.editFile, Start: m.editStart, End: m.editEnd, Text: text})
		m.message = "Comment added"
	}
	m.mode = modeNormal
	m.ta.Blur()
	m.anchor = -1
	m.layout()
	m.ensureVisible()
}

// ---------- viewed ----------

func (m *model) viewedCount() int {
	n := 0
	for _, v := range m.viewed {
		if v {
			n++
		}
	}
	return n
}

func (m *model) toggleViewed() {
	fi := m.curFile()
	m.viewed[fi] = !m.viewed[fi]
	if err := m.store.Set(m.files[fi].Hash, m.viewed[fi]); err != nil {
		m.message = "Could not save viewed state: " + err.Error()
	}
	m.anchor = -1
	m.collapsed[fi] = m.viewed[fi]
	m.cursor = m.fileItem[fi]
	m.layout()
	if m.viewed[fi] {
		// move on to the next file still awaiting review
		next := -1
		for j := 1; j < len(m.files); j++ {
			k := (fi + j) % len(m.files)
			if !m.viewed[k] {
				next = k
				break
			}
		}
		if next >= 0 && next > fi {
			m.jumpToFile(next)
		} else if fi+1 < len(m.files) {
			m.jumpToFile(fi + 1)
		} else {
			m.ensureVisible()
		}
		if m.viewedCount() == len(m.files) {
			m.message = "All files viewed 🎉  press q to finish"
		}
	} else {
		m.ensureVisible()
	}
}

func (m *model) toggleCollapse(fi int) {
	m.collapsed[fi] = !m.collapsed[fi]
	m.cursor = m.fileItem[fi]
	m.anchor = -1
	m.layout()
	m.ensureVisible()
}

// ---------- update ----------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		wasFull := m.fileListFull()
		m.width, m.height = msg.Width, msg.Height
		if m.fileListFull() && !wasFull {
			m.fileSel = m.curFile()
		}
		m.ta.SetWidth(m.width)
		m.layout()
		m.ensureVisible()
		return m, nil

	case highlightMsg:
		if msg.err != nil {
			m.deltaErrors++
			if m.firstDeltaErr == "" {
				m.firstDeltaErr = msg.err.Error()
			}
			return m, nil
		}
		f := m.files[msg.file]
		for i := range f.Lines {
			f.Lines[i].Text, f.Lines[i].Fill, f.Lines[i].Width = msg.lines[i].Text, msg.lines[i].Fill, msg.lines[i].Width
		}
		if m.wrap {
			m.layout()
		}
		return m, nil

	case tea.MouseMsg:
		return m, m.handleMouse(msg)

	case tea.KeyMsg:
		if m.mode == modeComment {
			switch msg.String() {
			case "esc":
				m.mode = modeNormal
				m.ta.Blur()
				m.message = "Comment discarded"
				m.ensureVisible()
				return m, nil
			case "enter", "ctrl+s":
				m.saveComment()
				return m, nil
			case "ctrl+c":
				return m, m.tryAbort()
			}
			var cmd tea.Cmd
			m.ta, cmd = m.ta.Update(msg)
			return m, cmd
		}
		if m.mode == modeHelp {
			if msg.String() == "ctrl+c" {
				return m, m.tryAbort()
			}
			if msg.String() == "q" {
				m.printOutput = true
				return m, tea.Quit
			}
			m.mode = modeNormal
			return m, nil
		}
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
			// several keys can arrive in one read (key repeat, fast typing)
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				cmds = append(cmds, m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt}))
			}
			return m, tea.Batch(cmds...)
		}
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *model) tryAbort() tea.Cmd {
	if len(m.comments) > 0 && !m.pendingQuit {
		m.pendingQuit = true
		m.message = fmt.Sprintf("Press ctrl+c again to discard %d comment(s) — q quits and prints them", len(m.comments))
		return nil
	}
	return tea.Quit
}

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.fileListFull() {
		return m.handleFileListKey(msg.String())
	}
	k := msg.String()
	if k != "d" && k != "x" {
		m.pendingDelete = false
	}
	if k != "ctrl+c" {
		m.pendingQuit = false
	}
	m.message = ""
	h := m.viewH()

	switch k {
	case "q":
		m.printOutput = true
		return tea.Quit
	case "ctrl+c":
		return m.tryAbort()
	case "?":
		m.mode = modeHelp
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "ctrl+d":
		m.moveRows(h / 2)
	case "ctrl+u":
		m.moveRows(-h / 2)
	case "ctrl+f", "pgdown", " ":
		m.moveRows(h - 2)
	case "ctrl+b", "pgup", "b":
		m.moveRows(-(h - 2))
	case "ctrl+e":
		m.scroll(1)
	case "ctrl+y":
		m.scroll(-1)
	case "g", "home":
		m.cursor = 0
		m.offset = 0
		m.ensureVisible()
	case "G", "end":
		if it := m.selectableAtOrBefore(len(m.rows) - 1); it >= 0 {
			m.cursor = it
		}
		m.offset = len(m.rows)
		m.clampOffset()
		m.ensureVisible()
	case "n", "]":
		if fi := m.curFile(); fi+1 < len(m.files) {
			m.jumpToFile(fi + 1)
		}
	case "p", "[":
		fi := m.curFile()
		if m.cursor != m.fileItem[fi] {
			m.jumpToFile(fi)
		} else if fi > 0 {
			m.jumpToFile(fi - 1)
		}
	case "}":
		m.jumpHunk(1)
	case "{":
		m.jumpHunk(-1)
	case "h", "left":
		if !m.wrap {
			m.hoff = max(0, m.hoff-8)
		}
	case "l", "right":
		if !m.wrap {
			m.hoff += 8
		}
	case "0":
		m.hoff = 0
	case "w":
		m.wrap = !m.wrap
		m.hoff = 0
		m.layout()
		m.ensureVisible()
		if m.wrap {
			m.message = "Word wrap on"
		} else {
			m.message = "Word wrap off"
		}
	case "e":
		m.showFiles = !m.showFiles
		m.fileSel = m.curFile()
		m.lastSideFile = -1
		m.layout()
		m.ensureVisible()
	case "#":
		m.showNums = !m.showNums
		m.layout()
		m.ensureVisible()
	case "v":
		m.toggleViewed()
	case "V":
		if m.anchor >= 0 {
			m.anchor = -1
		} else if m.items[m.cursor].kind == itemLine {
			m.anchor = m.cursor
		} else {
			m.message = "Move the cursor to a diff line to start a selection"
		}
	case "esc":
		m.anchor = -1
	case "enter", "c":
		if m.items[m.cursor].kind == itemFile {
			if k == "enter" {
				m.toggleCollapse(m.curFile())
			} else {
				m.message = "Move the cursor to a diff line to comment"
			}
			return nil
		}
		return m.startComment()
	case "z", "o":
		m.toggleCollapse(m.curFile())
	case "Z":
		// collapse everything, or expand everything if all are collapsed
		all := true
		for _, c := range m.collapsed {
			all = all && c
		}
		for i := range m.collapsed {
			m.collapsed[i] = !all
		}
		m.cursor = m.fileItem[m.curFile()]
		m.layout()
		m.ensureVisible()
	case "d", "x":
		ci := m.commentAtCursor()
		if ci < 0 {
			m.message = "No comment on this line"
			return nil
		}
		if !m.pendingDelete {
			m.pendingDelete = true
			m.message = "Press " + k + " again to delete this comment"
			return nil
		}
		m.pendingDelete = false
		m.comments = append(m.comments[:ci], m.comments[ci+1:]...)
		m.layout()
		m.message = "Comment deleted"
	}
	return nil
}

func (m *model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	sw := m.sidebarW()
	full := m.fileListFull()
	inSidebar := full || (sw > 0 && msg.X < sw)
	h := m.viewH()

	if tea.MouseEvent(msg).IsWheel() {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if inSidebar {
				m.sideOff = max(0, m.sideOff-3)
			} else {
				m.scroll(-3)
			}
		case tea.MouseButtonWheelDown:
			if inSidebar {
				m.sideOff = max(0, min(m.sideOff+3, len(m.sideRows)-h))
			} else {
				m.scroll(3)
			}
		case tea.MouseButtonWheelLeft:
			if !m.wrap {
				m.hoff = max(0, m.hoff-4)
			}
		case tea.MouseButtonWheelRight:
			if !m.wrap {
				m.hoff += 4
			}
		}
		return nil
	}
	if m.mode != modeNormal {
		return nil
	}

	if msg.Action == tea.MouseActionRelease {
		if m.dragging {
			m.dragging = false
			if m.anchor == m.cursor {
				m.anchor = -1
			}
		}
		return nil
	}

	if msg.Action == tea.MouseActionMotion {
		if !m.dragging || m.anchor < 0 {
			return nil
		}
		y := msg.Y
		if y <= 0 {
			m.offset = max(0, m.offset-1)
			y = 1
		} else if y >= h-1 {
			m.offset++
			m.clampOffset()
			y = h - 1
		}
		r := m.offset + y
		if r >= 0 && r < len(m.rows) {
			it := m.rows[r].item
			if m.items[it].kind == itemLine && m.items[it].file == m.items[m.anchor].file && m.selectable(it) {
				m.cursor = it
			}
		}
		return nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || msg.Y >= h {
		return nil
	}
	m.message = ""

	if inSidebar {
		r := m.sideOff + msg.Y
		if r < len(m.sideRows) && m.sideRows[r] >= 0 {
			fi := m.sideRows[r]
			if full {
				m.openFile(fi)
				return nil
			}
			if m.collapsed[fi] {
				m.collapsed[fi] = false
				m.layout()
			}
			m.jumpToFile(fi)
		}
		return nil
	}
	if msg.X < m.diffX() {
		return nil
	}

	r := m.offset + msg.Y
	if msg.Y == 0 && !m.isHeaderRow(m.offset) && m.offset < len(m.rows) {
		// sticky header
		fi := m.items[m.rows[m.offset].item].file
		m.toggleCollapse(fi)
		m.offset = m.itemRow[m.fileItem[fi]]
		m.clampOffset()
		return nil
	}
	if r >= len(m.rows) {
		return nil
	}
	rw := m.rows[r]
	it := m.items[rw.item]
	switch it.kind {
	case itemFile:
		m.toggleCollapse(it.file)
	case itemLine:
		if !m.selectable(rw.item) {
			return nil
		}
		if msg.Shift && m.items[m.cursor].kind == itemLine && m.items[m.cursor].file == it.file {
			if m.anchor < 0 {
				m.anchor = m.cursor
			}
			m.cursor = rw.item
			return nil
		}
		m.cursor = rw.item
		if rw.comment < 0 {
			m.anchor = rw.item
			m.dragging = true
		} else {
			m.anchor = -1
		}
	}
	return nil
}

// handleFileListKey handles keys while the file list fills the screen.
func (m *model) handleFileListKey(k string) tea.Cmd {
	if k != "ctrl+c" {
		m.pendingQuit = false
	}
	m.message = ""
	switch k {
	case "q":
		m.printOutput = true
		return tea.Quit
	case "ctrl+c":
		return m.tryAbort()
	case "?":
		m.mode = modeHelp
	case "j", "down", "n", "]", "tab":
		m.fileSel = min(m.fileSel+1, len(m.files)-1)
	case "k", "up", "p", "[", "shift+tab":
		m.fileSel = max(m.fileSel-1, 0)
	case "g", "home":
		m.fileSel = 0
	case "G", "end":
		m.fileSel = len(m.files) - 1
	case "enter", "l", "right", "o", " ":
		m.openFile(m.fileSel)
	case "e", "esc", "h", "left":
		m.showFiles = false
		m.layout()
		m.ensureVisible()
	}
	return nil
}

// openFile closes the full-screen file list and jumps to file fi, expanding it.
func (m *model) openFile(fi int) {
	m.showFiles = false
	m.collapsed[fi] = false
	m.anchor = -1
	m.layout()
	m.jumpToFile(fi)
}
