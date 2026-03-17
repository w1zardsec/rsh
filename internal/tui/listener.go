package tui

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	listenerPkg "codeberg.org/asuna/rsh/internal/listener"
)

// ── Messages ─────────────────────────────────────────────────────────────────

type listenerStartedMsg struct {
	listenerID int
	iface      string
	port       string
	err        error
}

type listenerStoppedMsg struct {
	listenerID int
	err        error
}

// ── Interface picker ──────────────────────────────────────────────────────────

// NetIface holds a display name and the IP address to bind on.
type NetIface struct {
	Name string // e.g. "eth0", "all"
	Addr string // e.g. "192.168.1.5", "0.0.0.0"
}

func (n NetIface) Display() string {
	if n.Addr == "0.0.0.0" {
		return "all  " + mutedStyle.Render("(0.0.0.0)")
	}
	return n.Name + "  " + mutedStyle.Render("("+n.Addr+")")
}

func loadInterfaces() []NetIface {
	result := []NetIface{{Name: "all", Addr: "0.0.0.0"}}
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				result = append(result, NetIface{Name: iface.Name, Addr: ip4.String()})
				break
			}
		}
	}
	return result
}

// ── Model ─────────────────────────────────────────────────────────────────────

type ListenerModel struct {
	manager *listenerPkg.Manager

	// Interface picker
	ifaceOptions []NetIface
	ifaceIdx     int

	// Port input
	portInput textinput.Model
	formField int // 0 = iface picker, 1 = port
	shellCh   chan listenerPkg.ShellConnectedMsg

	// Mode
	insertMode bool
	lastKey    string

	// Table
	listenerTbl table.Model

	// Status
	statusMsg string
	statusOK  bool

	width  int
	height int
}

func NewListenerModel(manager *listenerPkg.Manager, shellCh chan listenerPkg.ShellConnectedMsg) ListenerModel {
	pi := textinput.New()
	pi.Placeholder = "4444"
	pi.CharLimit = 5
	pi.Prompt = ""

	// Listener table
	ltCols := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Interface", Width: 20},
		{Title: "Port", Width: 6},
		{Title: "Status", Width: 11},
		{Title: "Shells", Width: 6},
	}
	lt := table.New(
		table.WithColumns(ltCols),
		table.WithFocused(true),
		table.WithHeight(4),
	)
	lt.SetStyles(listenerTableStyles())

	return ListenerModel{
		manager:      manager,
		shellCh:      shellCh,
		ifaceOptions: loadInterfaces(),
		portInput:    pi,
		listenerTbl:  lt,
	}
}

func listenerTableStyles() table.Styles {
	ts := table.DefaultStyles()
	ts.Header = ts.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorder).
		BorderBottom(true).
		Foreground(colorFocused).
		Bold(false)
	ts.Selected = ts.Selected.
		Foreground(lipgloss.Color("#000000")).
		Background(colorFocused).
		Bold(true)
	ts.Cell = ts.Cell.Foreground(colorTitle)
	return ts
}

// ── Commands ──────────────────────────────────────────────────────────────────

func (m ListenerModel) cmdStartListener() tea.Cmd {
	opt := m.ifaceOptions[m.ifaceIdx]
	port := m.portInput.Value()
	if port == "" {
		port = "4444"
	}
	ch := m.shellCh
	return func() tea.Msg {
		id, err := m.manager.Start(opt.Addr, port, func(msg listenerPkg.ShellConnectedMsg) {
			ch <- msg
		})
		return listenerStartedMsg{listenerID: id, iface: opt.Addr, port: port, err: err}
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m ListenerModel) Init() tea.Cmd { return nil }

func (m ListenerModel) Update(msg tea.Msg) (ListenerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case listenerStartedMsg:
		return m.handleStartedMsg(msg)
	case tea.KeyMsg:
		if m.insertMode {
			return m.handleInsertMode(msg)
		}
		return m.handleNormalMode(msg)
	}
	var cmd tea.Cmd
	m.listenerTbl, cmd = m.listenerTbl.Update(msg)
	return m, cmd
}

func (m ListenerModel) handleStartedMsg(msg listenerStartedMsg) (ListenerModel, tea.Cmd) {
	if msg.err != nil {
		m.statusMsg = msg.err.Error()
		m.statusOK = false
	} else {
		m.statusMsg = fmt.Sprintf("Listener #%d started (%s:%s)", msg.listenerID, msg.iface, msg.port)
		m.statusOK = true
		m.portInput.SetValue("")
		m.insertMode = false
		m.portInput.Blur()
	}
	m.refreshTable()
	return m, nil
}

func (m ListenerModel) handleNormalMode(msg tea.KeyMsg) (ListenerModel, tea.Cmd) {
	key := msg.String()
	defer func() { m.lastKey = key }()

	switch key {
	case "i", "a":
		m.formField = 0
		m.insertMode = true
		m.statusMsg = ""
		m.ifaceOptions = loadInterfaces()
		m.portInput.Blur()
		return m, textinput.Blink

	case "j", "down":
		m.listenerTbl.MoveDown(1)
	case "k", "up":
		m.listenerTbl.MoveUp(1)
	case "g":
		if m.lastKey == "g" {
			m.listenerTbl.GotoTop()
		}
	case "G":
		m.listenerTbl.GotoBottom()
	case "ctrl+d":
		m.listenerTbl.MoveDown(5)
	case "ctrl+u":
		m.listenerTbl.MoveUp(5)

	case "d", "x":
		return m.stopSelected()

	case "r":
		m.refreshTable()
		m.statusMsg = "Refreshed."
		m.statusOK = true
	}
	return m, nil
}

func (m ListenerModel) handleInsertMode(msg tea.KeyMsg) (ListenerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.insertMode = false
		m.portInput.Blur()
		return m, nil

	case "tab", "down":
		return m.cycleFormField(1)
	case "shift+tab", "up":
		return m.cycleFormField(-1)

	case "enter":
		if m.formField == 0 {
			return m.cycleFormField(1)
		}
		return m, m.cmdStartListener()
	}

	// Field 0: interface picker — h/l to cycle
	if m.formField == 0 {
		switch msg.String() {
		case "h", "left":
			m.ifaceIdx = (m.ifaceIdx - 1 + len(m.ifaceOptions)) % len(m.ifaceOptions)
		case "l", "right":
			m.ifaceIdx = (m.ifaceIdx + 1) % len(m.ifaceOptions)
		}
		return m, nil
	}

	// Field 1: port textinput
	var cmd tea.Cmd
	m.portInput, cmd = m.portInput.Update(msg)
	return m, cmd
}

func (m ListenerModel) cycleFormField(dir int) (ListenerModel, tea.Cmd) {
	m.formField = (m.formField + dir + 2) % 2
	if m.formField == 1 {
		m.portInput.Focus()
	} else {
		m.portInput.Blur()
	}
	return m, textinput.Blink
}

func (m *ListenerModel) stopSelected() (ListenerModel, tea.Cmd) {
	row := m.listenerTbl.SelectedRow()
	if len(row) == 0 {
		m.statusMsg = "No listener selected."
		m.statusOK = false
		return *m, nil
	}
	var id int
	fmt.Sscanf(strings.TrimSpace(row[0]), "%d", &id)
	err := m.manager.Stop(id)
	if err != nil {
		m.statusMsg = err.Error()
		m.statusOK = false
	} else {
		m.statusMsg = fmt.Sprintf("Listener #%d stopped.", id)
		m.statusOK = true
	}
	m.refreshTable()
	return *m, func() tea.Msg { return listenerStoppedMsg{listenerID: id, err: err} }
}

func (m *ListenerModel) refreshTable() {
	entries := m.manager.Entries()
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	rows := make([]table.Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, table.Row{
			fmt.Sprintf("%d", e.ID),
			e.Interface,
			e.Port,
			string(e.Status),
			fmt.Sprintf("%d", e.ShellCount()),
		})
	}
	m.listenerTbl.SetRows(rows)
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m ListenerModel) View() string {
	w := m.width
	if w < 60 {
		w = 60
	}

	// Resize table columns to fit current terminal width.
	// Fixed cols: ID(4)+Port(6)+Status(11)+Shells(6) = 27; Interface stretches.
	innerW := w - 2
	ifaceW := innerW - 27
	if ifaceW < 8 {
		ifaceW = 8
	}
	m.listenerTbl.SetColumns([]table.Column{
		{Title: "ID", Width: 4},
		{Title: "Interface", Width: ifaceW},
		{Title: "Port", Width: 6},
		{Title: "Status", Width: 11},
		{Title: "Shells", Width: 6},
	})

	// Form pane (always 3 lines: border-top + content + border-bot)
	formPane := pane(m.formTitle(), m.renderFormContent(innerW), w, m.insertMode)

	// Status line
	statusLine := m.renderStatus()
	statusH := 0
	if statusLine != "" {
		statusH = 1
	}

	// Available height for the listener table pane.
	// Total = header(2) + gap(1) + form(3) + gap(1) + [status(1)+gap(1)] + ltPane + footer(2)
	// => avail = height - 9 - statusH
	avail := m.height - 9 - statusH
	if avail < 6 {
		avail = 6
	}

	m.listenerTbl.SetHeight(avail - 4) // pane borders(2) + table-header+divider(2)

	ltPane := pane("Active Listeners", m.renderListenerTableContent(w-2), w, !m.insertMode)

	parts := []string{"", formPane, ""}
	if statusLine != "" {
		parts = append(parts, statusLine, "")
	}
	parts = append(parts, ltPane)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// formTitle returns the pane title with the mode badge.
func (m ListenerModel) formTitle() string {
	if m.insertMode {
		return "New Listener  " + insertBadgeStyle.Render("INSERT")
	}
	return "New Listener  " + normalBadgeStyle.Render("NORMAL")
}

func (m ListenerModel) renderFormContent(innerW int) string {
	// Interface picker display
	ifLabel := mutedStyle.Render("Interface")
	opt := m.ifaceOptions[m.ifaceIdx]
	var ifVal string

	if m.insertMode && m.formField == 0 {
		ifLabel = focusedLabelStyle.Render("Interface")
		arrow := focusedLabelStyle.Render("‹")
		optText := valueStyle.Render(opt.Name) + "  " + mutedStyle.Render("("+opt.Addr+")")
		arrowR := focusedLabelStyle.Render("›")
		ifVal = arrow + " " + optText + " " + arrowR
	} else {
		ifVal = mutedStyle.Render(opt.Name + "  (" + opt.Addr + ")")
		if m.insertMode {
			ifVal = valueStyle.Render(opt.Name + "  (" + opt.Addr + ")")
		}
	}

	// Port field
	ptLabel := mutedStyle.Render("Port")
	var ptVal string
	if m.insertMode && m.formField == 1 {
		ptLabel = focusedLabelStyle.Render("Port")
		ptVal = m.portInput.View()
	} else {
		pv := m.portInput.Value()
		if pv == "" {
			pv = m.portInput.Placeholder
		}
		if m.insertMode {
			ptVal = valueStyle.Render(pv)
		} else {
			ptVal = mutedStyle.Render(pv)
		}
	}

	row := fmt.Sprintf(" %s  %s      %s  %s", ifLabel, ifVal, ptLabel, ptVal)
	pad := innerW - lipgloss.Width(row)
	if pad < 0 {
		pad = 0
	}
	return row + strings.Repeat(" ", pad)
}

func (m ListenerModel) renderListenerTableContent(innerW int) string {
	if len(m.listenerTbl.Rows()) == 0 {
		row := " " + mutedStyle.Render("No active listeners.")
		pad := innerW - lipgloss.Width(row)
		if pad < 0 {
			pad = 0
		}
		return row + strings.Repeat(" ", pad) + "\n" + strings.Repeat(" ", innerW)
	}
	return m.listenerTbl.View()
}

func (m ListenerModel) renderStatus() string {
	if m.statusMsg == "" {
		return ""
	}
	if m.statusOK {
		return " " + successStyle.Render("+") + " " + mutedStyle.Render(m.statusMsg)
	}
	return " " + errorStyle.Render("-") + " " + mutedStyle.Render(m.statusMsg)
}
