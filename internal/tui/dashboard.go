package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	listenerPkg "codeberg.org/asuna/rsh/internal/listener"
)

// attachShellMsg is emitted when the user chooses "Interactive Shell" in the action modal.
type attachShellMsg struct {
	shell *listenerPkg.Shell
}

// shellDetachedMsg is emitted when the user exits an interactive shell session.
type shellDetachedMsg struct{}

// shellRemovedMsg is emitted when a connected client is deleted.
type shellRemovedMsg struct{ shellID int }

// uploadRequestMsg is emitted when the user confirms an upload.
type uploadRequestMsg struct {
	shell      *listenerPkg.Shell
	localPath  string
	remotePath string
}

// uploadDoneMsg is emitted when an upload finishes.
type uploadDoneMsg struct {
	shellID int
	err     error
}

// dashModal tracks which overlay (if any) is currently active.
type dashModal int

const (
	modalNone       dashModal = iota
	modalAction               // pick: Interactive Shell | Upload File
	modalUploadForm           // enter local + remote paths
	modalConfirmDel           // y/n confirm disconnect
)

// ActivityLevel controls the colour of an activity row.
type ActivityLevel string

const (
	LevelInfo    ActivityLevel = "info"
	LevelSuccess ActivityLevel = "success"
	LevelWarning ActivityLevel = "warning"
	LevelError   ActivityLevel = "error"
)

// ActivityEntry is a single log entry shown in the dashboard.
type ActivityEntry struct {
	Time    time.Time
	Message string
	Level   ActivityLevel
}

// DashboardModel is the model for the Dashboard tab.
type DashboardModel struct {
	manager     *listenerPkg.Manager
	activity    []ActivityEntry
	listenerTbl table.Model
	clientTbl   table.Model
	width       int
	height      int

	// modal state
	modal       dashModal
	modalCursor int
	activeShell *listenerPkg.Shell

	// upload form inputs
	uploadLocalInput  textinput.Model
	uploadRemoteInput textinput.Model
	uploadField       int // 0 = local, 1 = remote
}

func NewDashboardModel(manager *listenerPkg.Manager) DashboardModel {
	// Listener table – mirrors Listener tab but read-only
	ltCols := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Interface", Width: 15},
		{Title: "Port", Width: 6},
		{Title: "Status", Width: 11},
		{Title: "Shells", Width: 6},
	}
	lt := table.New(
		table.WithColumns(ltCols),
		table.WithFocused(false),
		table.WithHeight(2),
	)
	lt.SetStyles(dashTableStyles(false))

	// Connected clients table
	ctCols := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Remote", Width: 22},
		{Title: "Listener", Width: 8},
		{Title: "Connected", Width: 18},
	}
	ct := table.New(
		table.WithColumns(ctCols),
		table.WithFocused(true),
		table.WithHeight(2),
	)
	ct.SetStyles(dashTableStyles(true))

	ulIn := textinput.New()
	ulIn.Placeholder = "/home/user/shell.php"
	ulIn.Prompt = ""
	ulIn.Width = 40

	urIn := textinput.New()
	urIn.Placeholder = "/tmp/shell.php"
	urIn.Prompt = ""
	urIn.Width = 40

	return DashboardModel{
		manager:           manager,
		activity:          make([]ActivityEntry, 0),
		listenerTbl:       lt,
		clientTbl:         ct,
		uploadLocalInput:  ulIn,
		uploadRemoteInput: urIn,
	}
}

func dashTableStyles(focused bool) table.Styles {
	ts := table.DefaultStyles()
	hdrFg := colorFocused
	if !focused {
		hdrFg = colorMuted
	}
	ts.Header = ts.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorder).
		BorderBottom(true).
		Foreground(hdrFg).
		Bold(false)
	if focused {
		ts.Selected = ts.Selected.
			Foreground(lipgloss.Color("#000000")).
			Background(colorFocused).
			Bold(false)
	} else {
		// No highlight on read-only tables
		ts.Selected = ts.Selected.
			Foreground(colorTitle).
			Background(lipgloss.Color("")).
			Bold(false)
	}
	ts.Cell = ts.Cell.Foreground(colorTitle)
	return ts
}

// AddActivity prepends a new entry to the event-log.
func (m *DashboardModel) AddActivity(level ActivityLevel, msg string) {
	m.activity = append([]ActivityEntry{
		{Time: time.Now(), Message: msg, Level: level},
	}, m.activity...)
	if len(m.activity) > 200 {
		m.activity = m.activity[:200]
	}
}

// RefreshTables re-reads listeners and connected clients from the manager.
func (m *DashboardModel) RefreshTables() {
	entries := m.manager.Entries()
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	ltRows := make([]table.Row, 0, len(entries))
	for _, e := range entries {
		ltRows = append(ltRows, table.Row{
			fmt.Sprintf("%d", e.ID),
			e.Interface,
			e.Port,
			string(e.Status),
			fmt.Sprintf("%d", e.ShellCount()),
		})
	}
	m.listenerTbl.SetRows(ltRows)

	ctRows := make([]table.Row, 0)
	for _, e := range entries {
		for _, s := range e.Shells() {
			ctRows = append(ctRows, table.Row{
				fmt.Sprintf("%d", s.ID),
				s.RemoteAddr,
				fmt.Sprintf("#%d:%s", e.ID, e.Port),
				s.ConnectedAt.Format("15:04:05 Jan02"),
			})
		}
	}
	m.clientTbl.SetRows(ctRows)
}

func (m DashboardModel) Init() tea.Cmd { return nil }

func (m DashboardModel) Update(msg tea.Msg) (DashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.modal != modalNone {
			return m.handleModalKey(msg)
		}
		switch msg.String() {
		case "enter":
			shell := m.selectedShell()
			if shell == nil {
				return m, nil
			}
			m.activeShell = shell
			m.modal = modalAction
			m.modalCursor = 0
			return m, nil

		case "d":
			shell := m.selectedShell()
			if shell == nil {
				return m, nil
			}
			m.activeShell = shell
			m.modal = modalConfirmDel
			m.modalCursor = 0
			return m, nil

		default:
			var cmd tea.Cmd
			m.clientTbl, cmd = m.clientTbl.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// selectedShell returns the Shell for the currently highlighted client-table row, or nil.
func (m DashboardModel) selectedShell() *listenerPkg.Shell {
	if len(m.clientTbl.Rows()) == 0 {
		return nil
	}
	row := m.clientTbl.SelectedRow()
	if len(row) == 0 {
		return nil
	}
	shellID, err := strconv.Atoi(row[0])
	if err != nil {
		return nil
	}
	return m.manager.FindShell(shellID)
}

// handleModalKey routes key events to the active modal handler.
func (m DashboardModel) handleModalKey(msg tea.KeyMsg) (DashboardModel, tea.Cmd) {
	switch m.modal {
	case modalAction:
		return m.handleActionModalKey(msg)
	case modalUploadForm:
		return m.handleUploadFormKey(msg)
	case modalConfirmDel:
		return m.handleConfirmDelKey(msg)
	}
	return m, nil
}

func (m DashboardModel) handleActionModalKey(msg tea.KeyMsg) (DashboardModel, tea.Cmd) {
	const numOptions = 2
	switch msg.String() {
	case "j", "down":
		m.modalCursor = (m.modalCursor + 1) % numOptions
	case "k", "up":
		m.modalCursor = (m.modalCursor - 1 + numOptions) % numOptions
	case "enter":
		shell := m.activeShell
		if m.modalCursor == 0 {
			// Interactive Shell
			m.modal = modalNone
			m.activeShell = nil
			return m, func() tea.Msg { return attachShellMsg{shell: shell} }
		}
		// Upload File — transition to upload form
		m.modal = modalUploadForm
		m.uploadField = 0
		m.uploadLocalInput.SetValue("")
		m.uploadRemoteInput.SetValue("")
		m.uploadLocalInput.Focus()
		m.uploadRemoteInput.Blur()
		return m, textinput.Blink
	case "esc":
		m.modal = modalNone
		m.activeShell = nil
	}
	return m, nil
}

func (m DashboardModel) handleUploadFormKey(msg tea.KeyMsg) (DashboardModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.modal = modalNone
		m.activeShell = nil
		m.uploadLocalInput.Blur()
		m.uploadRemoteInput.Blur()
		return m, nil
	case "tab", "down":
		m.uploadField = (m.uploadField + 1) % 2
		if m.uploadField == 0 {
			m.uploadLocalInput.Focus()
			m.uploadRemoteInput.Blur()
		} else {
			m.uploadLocalInput.Blur()
			m.uploadRemoteInput.Focus()
		}
		return m, textinput.Blink
	case "shift+tab", "up":
		m.uploadField = (m.uploadField - 1 + 2) % 2
		if m.uploadField == 0 {
			m.uploadLocalInput.Focus()
			m.uploadRemoteInput.Blur()
		} else {
			m.uploadLocalInput.Blur()
			m.uploadRemoteInput.Focus()
		}
		return m, textinput.Blink
	case "enter":
		localPath := strings.TrimSpace(m.uploadLocalInput.Value())
		remotePath := strings.TrimSpace(m.uploadRemoteInput.Value())
		if localPath == "" || remotePath == "" {
			return m, nil
		}
		shell := m.activeShell
		m.modal = modalNone
		m.activeShell = nil
		m.uploadLocalInput.Blur()
		m.uploadRemoteInput.Blur()
		return m, func() tea.Msg {
			return uploadRequestMsg{shell: shell, localPath: localPath, remotePath: remotePath}
		}
	}
	var cmd tea.Cmd
	if m.uploadField == 0 {
		m.uploadLocalInput, cmd = m.uploadLocalInput.Update(msg)
	} else {
		m.uploadRemoteInput, cmd = m.uploadRemoteInput.Update(msg)
	}
	return m, cmd
}

func (m DashboardModel) handleConfirmDelKey(msg tea.KeyMsg) (DashboardModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m.confirmDelete()
	case "n", "N", "esc":
		m.modal = modalNone
		m.activeShell = nil
	case "h", "left":
		m.modalCursor = 0
	case "l", "right":
		m.modalCursor = 1
	case "enter":
		if m.modalCursor == 0 {
			return m.confirmDelete()
		}
		m.modal = modalNone
		m.activeShell = nil
	}
	return m, nil
}

func (m DashboardModel) confirmDelete() (DashboardModel, tea.Cmd) {
	shell := m.activeShell
	m.modal = modalNone
	m.activeShell = nil
	return m, func() tea.Msg { return shellRemovedMsg{shellID: shell.ID} }
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m DashboardModel) View() string {
	base := m.baseView()
	if m.modal != modalNone {
		w := m.width
		h := m.height - 4 // account for app header/footer
		if w < 60 {
			w = 80
		}
		if h < 10 {
			h = 20
		}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderModal())
	}
	return base
}

func (m DashboardModel) renderModal() string {
	switch m.modal {
	case modalAction:
		return m.renderActionModal()
	case modalUploadForm:
		return m.renderUploadFormModal()
	case modalConfirmDel:
		return m.renderConfirmDelModal()
	}
	return ""
}

func (m DashboardModel) renderActionModal() string {
	header := ""
	if m.activeShell != nil {
		header = titleStyle.Render(fmt.Sprintf("Shell #%d", m.activeShell.ID)) +
			"  " + mutedStyle.Render(m.activeShell.RemoteAddr)
	}

	options := []string{"Interactive Shell", "Upload File"}
	var rows []string
	for i, opt := range options {
		if i == m.modalCursor {
			rows = append(rows, focusedLabelStyle.Render("▸ "+opt))
		} else {
			rows = append(rows, mutedStyle.Render("  "+opt))
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		strings.Join(rows, "\n"),
		"",
		mutedStyle.Render("j/k navigate · Enter select · Esc close"),
	)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFocused).
		Padding(1, 3).
		Render(content)
}

func (m DashboardModel) renderUploadFormModal() string {
	header := ""
	if m.activeShell != nil {
		header = titleStyle.Render(fmt.Sprintf("Upload to Shell #%d", m.activeShell.ID)) +
			"  " + mutedStyle.Render(m.activeShell.RemoteAddr)
	}

	localLabel := mutedStyle.Render("Local Path  ")
	remoteLabel := mutedStyle.Render("Remote Path ")
	if m.uploadField == 0 {
		localLabel = focusedLabelStyle.Render("Local Path  ")
	} else {
		remoteLabel = focusedLabelStyle.Render("Remote Path ")
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		localLabel+m.uploadLocalInput.View(),
		remoteLabel+m.uploadRemoteInput.View(),
		"",
		mutedStyle.Render("Tab switch · Enter upload · Esc cancel"),
	)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFocused).
		Padding(1, 3).
		Width(62).
		Render(content)
}

func (m DashboardModel) renderConfirmDelModal() string {
	shellInfo := ""
	if m.activeShell != nil {
		shellInfo = m.activeShell.RemoteAddr
	}

	yesStyle := mutedStyle
	noStyle := mutedStyle
	if m.modalCursor == 0 {
		yesStyle = focusedLabelStyle
	} else {
		noStyle = focusedLabelStyle
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		errorStyle.Render("Remove this shell?"),
		mutedStyle.Render(shellInfo),
		"",
		mutedStyle.Render("This will close the connection."),
		"",
		yesStyle.Render("[Y] Yes")+"    "+noStyle.Render("[N] No"),
	)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorError).
		Padding(1, 3).
		Render(content)
}

func (m DashboardModel) baseView() string {
	w := m.width
	if w < 60 {
		w = 80
	}
	h := m.height
	if h < 20 {
		h = 20
	}

	// Available rows: total - header(2) - body-gap(1) - footer(2) = h-5.
	avail := h - 5
	if avail < 14 {
		avail = 14
	}

	// Two-column split: left 63 %, right rest
	leftW := w * 63 / 100
	rightW := w - leftW - 1
	if rightW < 22 {
		rightW = 22
		leftW = w - rightW - 1
	}

	left := m.renderLeftColumn(leftW, avail)
	right := m.renderEventLog(rightW, avail)

	columns := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	return lipgloss.JoinVertical(lipgloss.Left, "", columns)
}

// renderLeftColumn stacks three panes: Listeners / Connected Clients / Port Forwards.
func (m DashboardModel) renderLeftColumn(w, totalH int) string {
	innerW := w - 2
	if innerW < 10 {
		innerW = 10
	}

	// Resize listener table columns to fit current pane width.
	// Fixed cols: ID(4)+Port(6)+Status(11)+Shells(6) = 27; Interface stretches.
	ltIfaceW := innerW - 27
	if ltIfaceW < 8 {
		ltIfaceW = 8
	}
	m.listenerTbl.SetColumns([]table.Column{
		{Title: "ID", Width: 4},
		{Title: "Interface", Width: ltIfaceW},
		{Title: "Port", Width: 6},
		{Title: "Status", Width: 11},
		{Title: "Shells", Width: 6},
	})

	// Resize client table columns.
	// Fixed cols: ID(4)+Listener(9)+Connected(16) = 29; Remote stretches.
	ctRemoteW := innerW - 29
	if ctRemoteW < 8 {
		ctRemoteW = 8
	}
	m.clientTbl.SetColumns([]table.Column{
		{Title: "ID", Width: 4},
		{Title: "Remote", Width: ctRemoteW},
		{Title: "Listener", Width: 9},
		{Title: "Connected", Width: 16},
	})

	// Height allocation (rows, including pane borders)
	ltH := totalH * 27 / 100
	if ltH < 5 {
		ltH = 5
	}
	ctH := totalH * 42 / 100
	if ctH < 5 {
		ctH = 5
	}
	// Port-forwards pane consumes the rest; 2 rows are gaps between panes
	pfH := totalH - ltH - ctH - 2
	if pfH < 4 {
		pfH = 4
	}

	// Listener table — SetHeight sets visible data rows (header adds 2 extra)
	ltDataRows := ltH - 4
	if ltDataRows < 1 {
		ltDataRows = 1
	}
	m.listenerTbl.SetHeight(ltDataRows)
	ltContent := m.renderTable(&m.listenerTbl, "No active listeners.", innerW, ltH-2)
	ltPane := pane("Listeners", ltContent, w, false)

	// Connected clients table
	ctDataRows := ctH - 4
	if ctDataRows < 1 {
		ctDataRows = 1
	}
	m.clientTbl.SetHeight(ctDataRows)
	m.clientTbl.SetStyles(dashTableStyles(true))
	ctContent := m.renderTable(&m.clientTbl, "No connected clients.", innerW, ctH-2)
	ctPane := pane("Connected Clients", ctContent, w, false)

	// Port forwards (placeholder)
	pfContent := m.renderPortForwards(innerW, pfH-2)
	pfPane := pane("Port Forwards", pfContent, w, false)

	return lipgloss.JoinVertical(lipgloss.Left, ctPane, "", ltPane, "", pfPane)
}

// renderEventLog produces the right-column event-log pane.
func (m DashboardModel) renderEventLog(w, totalH int) string {
	innerH := totalH - 2
	if innerH < 1 {
		innerH = 1
	}
	content := m.renderActivity(w-2, innerH)
	return pane("Event Log", content, w, false)
}

// renderTable returns padded content for a read-only table inside a pane.
func (m DashboardModel) renderTable(t *table.Model, emptyMsg string, innerW, innerH int) string {
	var content string
	if len(t.Rows()) == 0 {
		row := " " + mutedStyle.Render(emptyMsg)
		pad := innerW - lipgloss.Width(row)
		if pad < 0 {
			pad = 0
		}
		content = row + strings.Repeat(" ", pad)
	} else {
		content = t.View()
	}
	return padLines(content, innerH, innerW)
}

func (m DashboardModel) renderPortForwards(innerW, innerH int) string {
	row := " " + mutedStyle.Render("No port forwards configured.")
	pad := innerW - lipgloss.Width(row)
	if pad < 0 {
		pad = 0
	}
	return padLines(row+strings.Repeat(" ", pad), innerH, innerW)
}

func (m DashboardModel) renderActivity(innerW, maxLines int) string {
	if maxLines < 1 {
		maxLines = 1
	}

	if len(m.activity) == 0 {
		line := " " + mutedStyle.Render("No activity yet.")
		return padLines(line, maxLines, innerW)
	}

	limit := len(m.activity)
	if limit > maxLines {
		limit = maxLines
	}

	var rows []string
	for _, a := range m.activity[:limit] {
		ts := mutedStyle.Render(a.Time.Format("15:04:05"))
		var indicator string
		switch a.Level {
		case LevelSuccess:
			indicator = successStyle.Render("+")
		case LevelError:
			indicator = errorStyle.Render("!")
		case LevelWarning:
			indicator = warningStyle.Render("~")
		default:
			indicator = mutedStyle.Render("·")
		}
		row := fmt.Sprintf(" %s  %s  %s", ts, indicator, a.Message)
		rightPad := innerW - lipgloss.Width(row)
		if rightPad < 0 {
			rightPad = 0
		}
		rows = append(rows, row+strings.Repeat(" ", rightPad))
	}

	return padLines(strings.Join(rows, "\n"), maxLines, innerW)
}

// padLines ensures content has exactly n lines, each exactly lineW cells wide.
func padLines(content string, n, lineW int) string {
	if lineW < 0 {
		lineW = 0
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		w := lipgloss.Width(l)
		if w > lineW {
			lines[i] = lipgloss.NewStyle().MaxWidth(lineW).Render(l)
		} else if w < lineW {
			lines[i] = l + strings.Repeat(" ", lineW-w)
		}
	}
	for len(lines) < n {
		lines = append(lines, strings.Repeat(" ", lineW))
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
