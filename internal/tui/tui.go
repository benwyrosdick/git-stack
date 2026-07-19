// Package tui is the interactive stack browser.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/benwyrosdick/git-stack/internal/gh"
	"github.com/benwyrosdick/git-stack/internal/git"
	"github.com/benwyrosdick/git-stack/internal/stack"
)

// Palette — muted selection bar; HEAD uses the bright accent.
const (
	colAccent   = "13" // magenta — checked-out branch
	colTitle    = "12" // blue
	colKey      = "14" // cyan
	colGreen    = "10"
	colYellow   = "11"
	colRed      = "9"
	colOrange   = "208"
	colDim      = "8"
	colMuted    = "245"
	colFg       = "252"
	colSelBg    = "236" // muted selection background
	colHeadMark = "13"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colTitle))
	// HEAD (checked out): accent name + ● (selection uses muted bar instead).
	headNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colAccent)).Bold(true)
	headMarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colHeadMark)).Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(colDim))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colDim))
	keyStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colKey))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(colRed))
	logStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	sepStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	// Selection row base (muted background).
	selBg = lipgloss.Color(colSelBg)
)

func styleOn(bg *lipgloss.Color, fg string, bold bool) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	if bold {
		s = s.Bold(true)
	}
	if bg != nil {
		s = s.Background(*bg)
	}
	return s
}

func statusStyled(s stack.BranchStatus, bg *lipgloss.Color) string {
	label := "[" + string(s) + "]"
	switch s {
	case stack.StatusOK:
		return styleOn(bg, colGreen, false).Render(label)
	case stack.StatusNeedsRestack:
		return styleOn(bg, colYellow, false).Render(label)
	case stack.StatusMissingParent:
		return styleOn(bg, colRed, false).Render(label)
	default:
		return styleOn(bg, colMuted, false).Render(label)
	}
}

func remoteStyled(r git.RemoteRelation, bg *lipgloss.Color) string {
	s := string(r)
	if s == "" {
		return ""
	}
	switch r {
	case git.RelInSync:
		return styleOn(bg, colGreen, false).Render(s)
	case git.RelBehind:
		return styleOn(bg, colYellow, false).Render(s)
	case git.RelDiverged:
		return styleOn(bg, colOrange, false).Render(s)
	case git.RelAhead:
		return styleOn(bg, colTitle, false).Render(s)
	case git.RelNone:
		return styleOn(bg, colDim, false).Render(s)
	default:
		return styleOn(bg, colDim, false).Render(s)
	}
}

// padRow extends content to width with spaces so a background fills the line.
func padRow(content string, width int, bg *lipgloss.Color) string {
	if width <= 0 {
		return content
	}
	w := lipgloss.Width(content)
	if w < width {
		pad := strings.Repeat(" ", width-w)
		if bg != nil {
			content += lipgloss.NewStyle().Background(*bg).Render(pad)
		} else {
			content += pad
		}
	}
	return content
}

// helpKeys colors key chords in a help string.
// Pass pairs: key, description, key, description, ...
func helpKeys(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteString(helpStyle.Render("  "))
		}
		b.WriteString(keyStyle.Render(pairs[i]))
		b.WriteString(helpStyle.Render(" " + pairs[i+1]))
	}
	return b.String()
}

// helpLine formats "  keys    description" for the help overlay.
func helpLine(keys, desc string) string {
	// pad keys column for alignment (plain length; styles expand)
	pad := 14
	if len(keys) < pad {
		return "  " + keyStyle.Render(keys) + helpStyle.Render(strings.Repeat(" ", pad-len(keys))+desc)
	}
	return "  " + keyStyle.Render(keys) + helpStyle.Render("  "+desc)
}

// Run starts the full-screen TUI.
func Run(repo *git.Repo, offline, refresh bool) error {
	m := newModel(repo, offline, refresh)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

type model struct {
	repo      *git.Repo
	eng       *stack.Engine
	offline   bool
	refresh   bool // force PR parent map refresh on next reload
	infos     []stack.BranchInfo
	cursor    int
	current   string
	status    string // busy / last action text
	lastMsg   string // single status line under help
	lastIsErr bool
	errMsg    string
	errScroll int // first visible line in error overlay
	showHelp  bool
	showError bool // full-screen multiline error overlay
	width     int
	height    int
	busy      bool
	input     inputMode
	prompt    string
	inputBuf  string
}

type inputMode int

const (
	inputNone inputMode = iota
	inputCreate
)

type loadedMsg struct {
	infos   []stack.BranchInfo
	current string
	err     error
}

type doneMsg struct {
	msg string
	err error
}

func newModel(repo *git.Repo, offline, refresh bool) model {
	var buf strings.Builder
	eng := &stack.Engine{Repo: repo, Out: &buf, Quiet: false}
	return model{
		repo:    repo,
		eng:     eng,
		offline: offline,
		refresh: refresh,
	}
}

func (m model) Init() tea.Cmd {
	return m.reload()
}

func (m model) reload() tea.Cmd {
	offline, refresh := m.offline, m.refresh
	eng := m.eng
	repo := m.repo
	return func() tea.Msg {
		_ = eng.LoadParents(stack.LoadParentsOpts{
			Offline: offline,
			Refresh: refresh,
		})
		infos, err := eng.List("")
		cur, _ := repo.CurrentBranch()
		return loadedMsg{infos: infos, current: cur, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case loadedMsg:
		m.busy = false
		m.refresh = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		prev := ""
		if m.cursor >= 0 && m.cursor < len(m.infos) {
			prev = m.infos[m.cursor].Name
		}
		m.infos = msg.infos
		m.current = msg.current
		// Keep selection on the same branch after reload; else land on HEAD.
		m.cursor = 0
		for i, info := range m.infos {
			if prev != "" && info.Name == prev {
				m.cursor = i
				break
			}
			if prev == "" && info.Name == m.current {
				m.cursor = i
			}
		}
		if m.cursor >= len(m.infos) {
			m.cursor = max(0, len(m.infos)-1)
		}
		return m, nil

	case doneMsg:
		m.busy = false
		if msg.err != nil {
			m.errMsg = strings.TrimRight(msg.err.Error(), "\n")
			m.errScroll = 0
			m.showError = true
			m.lastMsg = firstLine(m.errMsg)
			m.lastIsErr = true
			m.status = ""
		} else {
			m.errMsg = ""
			m.errScroll = 0
			m.showError = false
			m.lastIsErr = false
			if msg.msg != "" {
				m.status = msg.msg
				m.lastMsg = msg.msg
			}
		}
		return m, m.reload()

	case tea.KeyMsg:
		if m.busy {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.showError {
			return m.handleErrorKey(msg)
		}
		if m.input != inputNone {
			return m.handleInput(msg)
		}
		if m.showHelp {
			if msg.String() == "?" || msg.String() == "esc" || msg.String() == "q" {
				m.showHelp = false
			}
			return m, nil
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleErrorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	lines := errorLines(m.errMsg, m.width)
	page := errorPageSize(m.height)
	maxScroll := max(0, len(lines)-page)

	switch msg.String() {
	case "esc", "enter", "q":
		m.showError = false
		m.errScroll = 0
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.errScroll < maxScroll {
			m.errScroll++
		}
	case "k", "up":
		if m.errScroll > 0 {
			m.errScroll--
		}
	case "ctrl+d", "pgdown", "pgdn":
		m.errScroll = min(maxScroll, m.errScroll+page)
	case "ctrl+u", "pgup":
		m.errScroll = max(0, m.errScroll-page)
	case "g", "home":
		m.errScroll = 0
	case "G", "end":
		m.errScroll = maxScroll
	case " ":
		// space pages down; at bottom, dismiss
		if m.errScroll >= maxScroll {
			m.showError = false
			m.errScroll = 0
			return m, nil
		}
		m.errScroll = min(maxScroll, m.errScroll+page)
	}
	// clamp after resize / short content
	if m.errScroll > maxScroll {
		m.errScroll = maxScroll
	}
	return m, nil
}

func (m model) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		suffix := strings.TrimSpace(m.inputBuf)
		m.input = inputNone
		m.inputBuf = ""
		if suffix == "" {
			return m, nil
		}
		if m.cursor < 0 || m.cursor >= len(m.infos) {
			return m, nil
		}
		parent := m.infos[m.cursor].Name
		name := parent + "." + strings.TrimPrefix(suffix, ".")
		m.busy = true
		m.status = "creating " + name
		return m, func() tea.Msg {
			err := m.eng.Create(stack.CreateOpts{Name: name})
			if err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{msg: "created " + name}
		}
	case tea.KeyEsc:
		m.input = inputNone
		m.inputBuf = ""
		return m, nil
	case tea.KeyBackspace:
		if len(m.inputBuf) > 0 {
			m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
		}
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.inputBuf += string(msg.Runes)
		}
		return m, nil
	}
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.showHelp = true
		return m, nil
	case "j", "down":
		if m.cursor < len(m.infos)-1 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "g", "home":
		m.cursor = 0
		return m, nil
	case "G", "end":
		if len(m.infos) > 0 {
			m.cursor = len(m.infos) - 1
		}
		return m, nil
	case "enter":
		if len(m.infos) == 0 {
			return m, nil
		}
		b := m.infos[m.cursor].Name
		m.busy = true
		return m, func() tea.Msg {
			err := m.repo.Switch(b)
			if err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{msg: "checked out " + b}
		}
	case "r":
		return m.runRestack(false)
	case "R":
		return m.runRestack(true)
	case "s":
		return m.runSync(false, false)
	case "S":
		return m.runSync(true, false)
	case "d":
		return m.runDelete(false)
	case "D":
		return m.runDelete(true)
	case "f":
		m.busy = true
		m.status = "fetching origin…"
		return m, func() tea.Msg {
			if err := m.repo.FetchOrigin(); err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{msg: "fetched origin"}
		}
	case "F":
		if len(m.infos) == 0 {
			return m, nil
		}
		b := m.infos[m.cursor].Name
		m.busy = true
		m.status = "pulling " + b
		return m, func() tea.Msg {
			err := m.eng.Pull(b)
			if err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{msg: "pulled " + b}
		}
	case "p":
		if len(m.infos) == 0 {
			return m, nil
		}
		b := m.infos[m.cursor].Name
		m.busy = true
		return m, func() tea.Msg {
			err := m.eng.MaybePush(b, true)
			if err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{msg: "pushed " + b}
		}
	case "P":
		if len(m.infos) == 0 {
			return m, nil
		}
		b := m.infos[m.cursor].Name
		m.busy = true
		return m, func() tea.Msg {
			base := m.eng.ParentOf(b)
			url, err := gh.EnsurePR(m.repo, gh.PROpts{Branch: b, Base: base})
			if err != nil {
				return doneMsg{err: err}
			}
			m.eng.InvalidateParentCache()
			msg := "PR ready"
			if url != "" {
				msg = url
			}
			return doneMsg{msg: msg}
		}
	case "c":
		if len(m.infos) == 0 {
			return m, nil
		}
		m.input = inputCreate
		m.prompt = fmt.Sprintf("child of %s — suffix: ", m.infos[m.cursor].Name)
		m.inputBuf = ""
		return m, nil
	case "ctrl+r":
		m.busy = true
		m.refresh = true
		m.eng.InvalidateParentCache()
		return m, m.reload()
	}
	return m, nil
}

func (m model) runRestack(ontoTrunk bool) (tea.Model, tea.Cmd) {
	if len(m.infos) == 0 {
		return m, nil
	}
	b := m.infos[m.cursor].Name
	m.busy = true
	m.status = "restacking " + b
	return m, func() tea.Msg {
		err := m.eng.Restack(stack.RestackOpts{
			Branch:    b,
			OntoTrunk: ontoTrunk,
			NoFetch:   false,
		})
		if err != nil {
			return doneMsg{err: err}
		}
		return doneMsg{msg: "restacked " + b}
	}
}

func (m model) runSync(ontoTrunk, dryRun bool) (tea.Model, tea.Cmd) {
	if len(m.infos) == 0 {
		return m, nil
	}
	b := m.infos[m.cursor].Name
	m.busy = true
	m.status = "syncing " + b
	return m, func() tea.Msg {
		_, err := m.eng.Sync(stack.SyncOpts{
			Root:      b,
			OntoTrunk: ontoTrunk,
			DryRun:    dryRun,
		})
		if err != nil {
			return doneMsg{err: err}
		}
		msg := "synced " + b
		if dryRun {
			msg = "dry-run complete for " + b
		}
		return doneMsg{msg: msg}
	}
}

func (m model) runDelete(force bool) (tea.Model, tea.Cmd) {
	if len(m.infos) == 0 {
		return m, nil
	}
	b := m.infos[m.cursor].Name
	m.busy = true
	if force {
		m.status = "force-deleting " + b
	} else {
		m.status = "deleting " + b
	}
	return m, func() tea.Msg {
		err := m.eng.DeleteLocal(stack.DeleteOpts{Branch: b, Force: force})
		if err != nil {
			return doneMsg{err: err}
		}
		// Prefer landing cursor on parent after reload.
		msg := "deleted " + b
		if force {
			msg = "force-deleted " + b
		}
		return doneMsg{msg: msg}
	}
}

func (m model) View() string {
	if m.showError && m.errMsg != "" {
		return errorView(m.errMsg, m.width, m.height, m.errScroll)
	}
	if m.showHelp {
		return helpView()
	}
	var b strings.Builder

	// Header
	count := ""
	if n := len(m.infos); n > 0 {
		count = dimStyle.Render(fmt.Sprintf("  %d branches", n))
	}
	b.WriteString(titleStyle.Render("git-stack") + dimStyle.Render("  stacks") + count + "\n")
	if m.width > 0 {
		b.WriteString(sepStyle.Render(strings.Repeat("─", min(m.width, 80))) + "\n")
	} else {
		b.WriteString("\n")
	}

	if len(m.infos) == 0 {
		b.WriteString(dimStyle.Render("  no stacked branches") + "\n")
		b.WriteString(dimStyle.Render("  try: git-stack create feature && git-stack create child --from feature") + "\n")
	} else {
		for i, info := range m.infos {
			b.WriteString(m.renderRow(i, info) + "\n")
		}
	}

	b.WriteString("\n")
	if m.input != inputNone {
		b.WriteString(keyStyle.Render("create") + " " + m.prompt + m.inputBuf + "█\n")
	}

	if m.width > 0 {
		b.WriteString(sepStyle.Render(strings.Repeat("─", min(m.width, 80))) + "\n")
	}
	b.WriteString(helpKeys(
		"j/k", "move",
		"enter", "checkout",
		"r", "restack",
		"s", "sync",
		"R/S", "onto-trunk",
		"p", "push",
		"c", "create",
		"P", "pr",
		"d/D", "delete",
		"f/F", "fetch/pull",
		"?", "help",
		"q", "quit",
	) + "\n")

	// Single status line under help.
	switch {
	case m.busy:
		b.WriteString(dimStyle.Render("… " + truncateLine(m.status, m.width)))
	case m.lastMsg != "":
		line := truncateLine(m.lastMsg, m.width)
		if m.lastIsErr {
			b.WriteString(errStyle.Render(line))
		} else {
			b.WriteString(logStyle.Render(line))
		}
	}
	return b.String()
}

// renderRow draws one branch line. Selection = chevron + muted full-row bg.
// Checked-out branch uses the magenta accent (independent of selection).
func (m model) renderRow(i int, info stack.BranchInfo) string {
	selected := i == m.cursor
	isHead := info.Name == m.current

	var bg *lipgloss.Color
	if selected {
		c := selBg
		bg = &c
	}

	// Chevron / gutter
	var marker string
	if selected {
		marker = styleOn(bg, colAccent, true).Render(">") + styleOn(bg, colFg, false).Render(" ")
	} else {
		marker = styleOn(bg, colDim, false).Render("  ")
	}

	tree := styleOn(bg, colDim, false).Render(info.TreePrefix)

	// Name: HEAD gets accent; selection relies on bg rather than recoloring.
	var name string
	switch {
	case isHead:
		name = styleOn(bg, colAccent, true).Render(info.Name)
	case selected:
		name = styleOn(bg, colFg, true).Render(info.Name)
	default:
		name = styleOn(bg, colFg, false).Render(info.Name)
	}

	sha := styleOn(bg, colMuted, false).Render(info.ShortSHA)
	own := styleOn(bg, colMuted, false).Render("+" + info.OwnCommits)

	head := ""
	if isHead {
		head = styleOn(bg, colAccent, true).Render("  ●")
	}

	gap := styleOn(bg, colFg, false).Render("  ")
	line := marker + tree + name + gap + sha + gap + own + gap +
		statusStyled(info.Status, bg) + gap +
		remoteStyled(info.Remote, bg) + head

	if selected {
		line = padRow(line, m.width, bg)
	}
	return line
}

// errorLines wraps the full error into display lines.
func errorLines(msg string, width int) []string {
	wrapW := width
	if wrapW <= 0 {
		wrapW = 80
	}
	var lines []string
	for _, line := range strings.Split(msg, "\n") {
		lines = append(lines, wrapLine(line, wrapW)...)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// errorPageSize is how many body lines fit under title + footer.
func errorPageSize(height int) int {
	// title, blank, footer, blank margin
	n := height - 4
	if n < 3 {
		n = 3
	}
	return n
}

func errorView(msg string, width, height, scroll int) string {
	lines := errorLines(msg, width)
	page := errorPageSize(height)
	maxScroll := max(0, len(lines)-page)
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := min(len(lines), scroll+page)
	visible := lines[scroll:end]

	var b strings.Builder
	title := titleStyle.Render("git-stack") + "  " + errStyle.Render("error")
	if maxScroll > 0 {
		// 1-based line range for orientation
		title += dimStyle.Render(fmt.Sprintf("  (%d–%d / %d)", scroll+1, end, len(lines)))
	}
	b.WriteString(title + "\n\n")

	for _, line := range visible {
		b.WriteString(errStyle.Render(line) + "\n")
	}

	// pad so footer stays put when content is short
	for i := len(visible); i < page; i++ {
		b.WriteString("\n")
	}

	var hint strings.Builder
	if maxScroll > 0 {
		if scroll > 0 {
			hint.WriteString(dimStyle.Render("↑ more above  "))
		}
		if scroll < maxScroll {
			hint.WriteString(dimStyle.Render("↓ more below  "))
		}
		hint.WriteString(helpKeys(
			"j/k", "scroll",
			"space", "page",
			"g/G", "top/end",
		))
		hint.WriteString(helpStyle.Render("  ·  "))
	}
	hint.WriteString(helpKeys("esc/enter", "dismiss"))
	b.WriteString(hint.String())
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// wrapLine soft-wraps a single line to width (no newline collapsing).
func wrapLine(s string, width int) []string {
	if width < 20 {
		width = 20
	}
	if len(s) <= width {
		return []string{s}
	}
	var out []string
	for len(s) > width {
		// prefer break at space
		cut := width
		if i := strings.LastIndex(s[:width], " "); i > width/2 {
			cut = i + 1
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func helpView() string {
	section := func(title string) string {
		return titleStyle.Render(title) + "\n"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("git-stack help") + "\n\n")
	b.WriteString(section("Navigation"))
	b.WriteString(helpLine("j/k, ↑/↓", "move cursor") + "\n")
	b.WriteString(helpLine("g / G", "top / bottom") + "\n")
	b.WriteString(helpLine("enter", "checkout selected branch") + "\n\n")
	b.WriteString(section("Stack ops"))
	b.WriteString(helpLine("r", "restack selected onto parent") + "\n")
	b.WriteString(helpLine("R", "restack --onto-trunk") + "\n")
	b.WriteString(helpLine("s", "sync stack under selected") + "\n")
	b.WriteString(helpLine("S", "sync --onto-trunk") + "\n")
	b.WriteString(helpLine("d", "delete local branch (git branch -d)") + "\n")
	b.WriteString(helpLine("D", "force-delete local branch (git branch -D)") + "\n")
	b.WriteString(helpLine("c", "create child (suffix prompt)") + "\n")
	b.WriteString(helpLine("p", "push selected (force-with-lease)") + "\n")
	b.WriteString(helpLine("P", "create/retarget PR (gh)") + "\n")
	b.WriteString(helpLine("f", "fetch origin") + "\n")
	b.WriteString(helpLine("F", "pull selected (fetch + FF-only)") + "\n")
	b.WriteString(helpLine("ctrl+r", "refresh list") + "\n\n")
	b.WriteString(section("Other"))
	b.WriteString(helpLine("?", "toggle this help") + "\n")
	b.WriteString(helpLine("q", "quit") + "\n\n")
	b.WriteString(helpStyle.Render("  Stack: ") +
		styleOn(nil, colGreen, false).Render("ok") + helpStyle.Render(" · ") +
		styleOn(nil, colYellow, false).Render("needs-restack") + helpStyle.Render(" · ") +
		styleOn(nil, colRed, false).Render("missing-parent") + "\n")
	b.WriteString(helpStyle.Render("  Remote: ") +
		styleOn(nil, colGreen, false).Render("in-sync") + helpStyle.Render(" · ") +
		styleOn(nil, colYellow, false).Render("behind") + helpStyle.Render(" · ") +
		styleOn(nil, colOrange, false).Render("diverged") + helpStyle.Render(" · ") +
		styleOn(nil, colTitle, false).Render("ahead") + "\n")
	b.WriteString(helpStyle.Render("  Cursor: ") +
		styleOn(nil, colAccent, true).Render(">") + helpStyle.Render(" + muted bar  ·  HEAD: ") +
		headNameStyle.Render("name") + headMarkStyle.Render(" ●") + "\n\n")
	b.WriteString(helpStyle.Render("  Press ") + keyStyle.Render("?") +
		helpStyle.Render(" or ") + keyStyle.Render("esc") +
		helpStyle.Render(" to close.") + "\n")
	return b.String()
}

func truncateLine(s string, width int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if width > 10 && len(s) > width-2 {
		return s[:width-5] + "…"
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
