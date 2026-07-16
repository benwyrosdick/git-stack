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

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	// Selected row: chevron + accent text (no background bar), git-recent style.
	chevronStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	selNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	needStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	missStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	// Keys stand out from dim help labels (cyan/bright).
	keyStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	logStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	curStyle = lipgloss.NewStyle().Bold(true)
)

func statusStyled(s stack.BranchStatus) string {
	switch s {
	case stack.StatusOK:
		return okStyle.Render("[" + string(s) + "]")
	case stack.StatusNeedsRestack:
		return needStyle.Render("[" + string(s) + "]")
	case stack.StatusMissingParent:
		return missStyle.Render("[" + string(s) + "]")
	default:
		return "[" + string(s) + "]"
	}
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
func Run(repo *git.Repo) error {
	m := newModel(repo)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

type model struct {
	repo      *git.Repo
	eng       *stack.Engine
	infos     []stack.BranchInfo
	cursor    int
	current   string
	status    string
	errMsg    string
	errScroll int // first visible line in error overlay
	showHelp  bool
	showError bool // full-screen multiline error overlay
	log       []string
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

func newModel(repo *git.Repo) model {
	var buf strings.Builder
	eng := &stack.Engine{Repo: repo, Out: &buf, Quiet: false}
	return model{
		repo: repo,
		eng:  eng,
	}
}

func (m model) Init() tea.Cmd {
	return m.reload()
}

func (m model) reload() tea.Cmd {
	return func() tea.Msg {
		infos, err := m.eng.List("")
		cur, _ := m.repo.CurrentBranch()
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
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.infos = msg.infos
		m.current = msg.current
		if m.cursor >= len(m.infos) {
			m.cursor = max(0, len(m.infos)-1)
		}
		return m, nil

	case doneMsg:
		m.busy = false
		if msg.err != nil {
			m.errMsg = strings.TrimRight(msg.err.Error(), "\n")
			m.errScroll = 0 // start at top so summary is visible
			m.showError = true
			// One short log line for the footer history
			first := firstLine(m.errMsg)
			m.log = append(m.log, "error: "+first)
		} else {
			m.errMsg = ""
			m.errScroll = 0
			m.showError = false
			if msg.msg != "" {
				m.status = msg.msg
				m.log = append(m.log, msg.msg)
			}
		}
		if len(m.log) > 20 {
			m.log = m.log[len(m.log)-20:]
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
		return m.runSync(false, true)
	case "f":
		m.busy = true
		m.status = "fetching origin…"
		return m, func() tea.Msg {
			if err := m.repo.FetchOrigin(); err != nil {
				return doneMsg{err: err}
			}
			return doneMsg{msg: "fetched origin"}
		}
	case "P":
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
	case "p":
		if len(m.infos) == 0 {
			return m, nil
		}
		b := m.infos[m.cursor].Name
		m.busy = true
		return m, func() tea.Msg {
			url, err := gh.EnsurePR(m.eng, m.repo, gh.PROpts{Branch: b})
			if err != nil {
				return doneMsg{err: err}
			}
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

func (m model) View() string {
	if m.showError && m.errMsg != "" {
		return errorView(m.errMsg, m.width, m.height, m.errScroll)
	}
	if m.showHelp {
		return helpView()
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("git-stack") + dimStyle.Render("  stacked branches") + "\n\n")

	if len(m.infos) == 0 {
		b.WriteString(dimStyle.Render("  no dot-stacked local branches (e.g. feature.ui)") + "\n")
		b.WriteString(dimStyle.Render("  create one: git-stack create feature && git-stack create feature.ui") + "\n")
	} else {
		minDepth := m.infos[0].Depth
		for _, info := range m.infos {
			if info.Depth < minDepth {
				minDepth = info.Depth
			}
		}
		for i, info := range m.infos {
			indent := strings.Repeat("  ", info.Depth-minDepth)
			selected := i == m.cursor
			// Chevron like git-recent: ">" on selection, space otherwise.
			marker := "  "
			if selected {
				marker = chevronStyle.Render(">") + " "
			}

			name := info.Name
			switch {
			case selected:
				name = selNameStyle.Render(info.Name)
			case info.Name == m.current:
				name = curStyle.Render(info.Name)
			}

			sha := info.ShortSHA
			own := "+" + info.OwnCommits
			if selected {
				sha = selNameStyle.Render(sha)
				own = selNameStyle.Render(own)
			}

			head := ""
			if info.Name == m.current {
				head = dimStyle.Render("  ← HEAD")
			}

			// Status colors always applied — never overridden by selection.
			line := marker + indent + name + "  " + sha + "  " + own + "  " +
				statusStyled(info.Status) + "  " +
				dimStyle.Render(string(info.Remote)) + head
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	if m.input != inputNone {
		b.WriteString(m.prompt + m.inputBuf + "█\n")
	} else if m.busy {
		b.WriteString(dimStyle.Render("… "+m.status) + "\n")
	} else if m.status != "" {
		b.WriteString(logStyle.Render(m.status) + "\n")
	}

	if len(m.log) > 0 {
		b.WriteString(dimStyle.Render("─") + "\n")
		start := 0
		if len(m.log) > 3 {
			start = len(m.log) - 3
		}
		for _, l := range m.log[start:] {
			b.WriteString(dimStyle.Render(truncateLine(l, m.width)) + "\n")
		}
	}

	b.WriteString("\n" + helpKeys(
		"j/k", "move",
		"enter", "checkout",
		"r", "restack",
		"s", "sync",
		"R/S", "onto-trunk",
		"p", "pr",
		"c", "create",
		"P", "push",
		"f", "fetch",
		"d", "dry-run",
		"?", "help",
		"q", "quit",
	))
	return b.String()
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
	b.WriteString(helpLine("d", "dry-run sync plan") + "\n")
	b.WriteString(helpLine("c", "create child (suffix prompt)") + "\n")
	b.WriteString(helpLine("P", "push selected (force-with-lease)") + "\n")
	b.WriteString(helpLine("p", "create/retarget PR (gh)") + "\n")
	b.WriteString(helpLine("f", "fetch origin") + "\n")
	b.WriteString(helpLine("ctrl+r", "refresh list") + "\n\n")
	b.WriteString(section("Other"))
	b.WriteString(helpLine("?", "toggle this help") + "\n")
	b.WriteString(helpLine("q", "quit") + "\n\n")
	b.WriteString(helpStyle.Render("  Status: ") +
		okStyle.Render("ok") + helpStyle.Render(" · ") +
		needStyle.Render("needs-restack") + helpStyle.Render(" · ") +
		missStyle.Render("missing-parent") + "\n\n")
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
