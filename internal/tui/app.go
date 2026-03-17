package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	listenerPkg "codeberg.org/asuna/rsh/internal/listener"
)

type tabIndex int

const (
	tabDashboard tabIndex = iota
	tabListener
	tabPayload
)

var tabLabels = []string{"Dashboard", "Listener", "Payload"}

// AppModel is the root Bubble Tea model.
type AppModel struct {
	activeTab tabIndex

	dashboard DashboardModel
	listener  ListenerModel
	payload   PayloadModel

	manager *listenerPkg.Manager
	shellCh chan listenerPkg.ShellConnectedMsg

	width  int
	height int
}

func NewApp() AppModel {
	manager := listenerPkg.NewManager()
	shellCh := make(chan listenerPkg.ShellConnectedMsg, 64)
	return AppModel{
		activeTab: tabDashboard,
		dashboard: NewDashboardModel(manager),
		listener:  NewListenerModel(manager, shellCh),
		payload:   NewPayloadModel(manager),
		manager:   manager,
		shellCh:   shellCh,
	}
}

// ── Init ─────────────────────────────────────────────────────────────────────

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		waitShellCmd(m.shellCh),
		m.cmdDefaultListener(),
	)
}

// cmdDefaultListener auto-starts a listener on 0.0.0.0:9001 at launch.
func (m AppModel) cmdDefaultListener() tea.Cmd {
	ch := m.shellCh
	manager := m.manager
	return func() tea.Msg {
		id, err := manager.Start("0.0.0.0", "9001", func(msg listenerPkg.ShellConnectedMsg) {
			ch <- msg
		})
		return listenerStartedMsg{listenerID: id, iface: "0.0.0.0", port: "9001", err: err}
	}
}

// waitShellCmd blocks until a shell connects then returns its message.
func waitShellCmd(ch chan listenerPkg.ShellConnectedMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// ── Update ───────────────────────────────────────────────────────────────────

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.dashboard.width = msg.Width
		m.dashboard.height = msg.Height
		m.listener.width = msg.Width
		m.listener.height = msg.Height
		m.payload.width = msg.Width
		m.payload.height = msg.Height
		return m, nil

	case listenerStartedMsg:
		var cmd tea.Cmd
		m.listener, cmd = m.listener.Update(msg)
		cmds = append(cmds, cmd)
		if msg.err == nil {
			m.dashboard.AddActivity(LevelSuccess,
				fmt.Sprintf("Listener #%d started on %s:%s", msg.listenerID, msg.iface, msg.port))
			m.dashboard.RefreshTables()
		} else {
			m.dashboard.AddActivity(LevelError,
				fmt.Sprintf("Failed to start listener: %s", msg.err.Error()))
		}
		return m, tea.Batch(cmds...)

	case listenerStoppedMsg:
		if msg.err == nil {
			m.dashboard.AddActivity(LevelWarning,
				fmt.Sprintf("Listener #%d stopped.", msg.listenerID))
			m.dashboard.RefreshTables()
		}
		return m, nil

	case listenerPkg.ShellConnectedMsg:
		m.dashboard.AddActivity(LevelSuccess,
			fmt.Sprintf("Shell #%d connected from %s (via Listener #%d)",
				msg.Shell.ID, msg.Shell.RemoteAddr, msg.ListenerID))
		m.dashboard.RefreshTables()
		m.listener.refreshTable()
		return m, waitShellCmd(m.shellCh)

	case attachShellMsg:
		return m, tea.Exec(
			NewInteractiveSession(msg.shell.Conn),
			func(err error) tea.Msg { return shellDetachedMsg{} },
		)

	case shellDetachedMsg:
		m.dashboard.AddActivity(LevelInfo, "Detached from interactive shell.")
		m.dashboard.RefreshTables()
		return m, nil

	case shellRemovedMsg:
		if err := m.manager.RemoveShell(msg.shellID); err == nil {
			m.dashboard.AddActivity(LevelWarning,
				fmt.Sprintf("Shell #%d removed.", msg.shellID))
			m.dashboard.RefreshTables()
			m.listener.refreshTable()
		}
		return m, nil

	case uploadRequestMsg:
		m.dashboard.AddActivity(LevelInfo,
			fmt.Sprintf("Uploading %s → Shell #%d (%s)…", msg.localPath, msg.shell.ID, msg.remotePath))
		shell := msg.shell
		localPath := msg.localPath
		remotePath := msg.remotePath
		return m, func() tea.Msg {
			err := uploadFile(shell.Conn, localPath, remotePath)
			return uploadDoneMsg{shellID: shell.ID, err: err}
		}

	case uploadDoneMsg:
		if msg.err != nil {
			m.dashboard.AddActivity(LevelError,
				fmt.Sprintf("Shell #%d — upload failed to send: %s", msg.shellID, msg.err))
		} else {
			m.dashboard.AddActivity(LevelSuccess,
				fmt.Sprintf("Shell #%d — upload sent (check shell for result).", msg.shellID))
		}
		return m, nil

	case tea.KeyMsg:
		inInsert := m.activeTab == tabListener && (m.listener.insertMode)

		if !inInsert {
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "1":
				m.activeTab = tabDashboard
				return m, nil
			case "2":
				m.activeTab = tabListener
				return m, nil
			case "3":
				m.activeTab = tabPayload
				return m, nil
			case "tab":
				m.activeTab = (m.activeTab + 1) % tabIndex(len(tabLabels))
				return m, nil
			case "shift+tab":
				m.activeTab = (m.activeTab - 1 + tabIndex(len(tabLabels))) % tabIndex(len(tabLabels))
				return m, nil
			}
		}
	}

	switch m.activeTab {
	case tabDashboard:
		var cmd tea.Cmd
		m.dashboard, cmd = m.dashboard.Update(msg)
		cmds = append(cmds, cmd)
	case tabListener:
		var cmd tea.Cmd
		m.listener, cmd = m.listener.Update(msg)
		cmds = append(cmds, cmd)
	case tabPayload:
		var cmd tea.Cmd
		m.payload, cmd = m.payload.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m AppModel) View() string {
	if m.width == 0 {
		return ""
	}

	header := m.renderHeader()

	var body string
	switch m.activeTab {
	case tabDashboard:
		body = m.dashboard.View()
	case tabListener:
		body = m.listener.View()
	case tabPayload:
		body = m.payload.View()
	}

	footer := m.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderHeader produces a single top line:
//
//	Dashboard | Listener                   rsh · v0.1.0
//	─────────────────────────────────────────────────────────────
func (m AppModel) renderHeader() string {
	// Tab section (left) — [Label] bracket style
	var tabParts []string
	for i, label := range tabLabels {
		if tabIndex(i) == m.activeTab {
			tabParts = append(tabParts,
				lipgloss.NewStyle().Foreground(colorDim).Render("[")+
					lipgloss.NewStyle().Foreground(colorFocused).Bold(true).Render(label)+
					lipgloss.NewStyle().Foreground(colorDim).Render("]"))
		} else {
			tabParts = append(tabParts,
				lipgloss.NewStyle().Foreground(colorDim).Render("[")+
					mutedStyle.Render(label)+
					lipgloss.NewStyle().Foreground(colorDim).Render("]"))
		}
	}
	tabs := strings.Join(tabParts, " ")

	// App title (right)
	title := titleStyle.Render("rsh") +
		mutedStyle.Render(" · v0.1.0")

	// Spacer
	tabsW := lipgloss.Width(tabs)
	titleW := lipgloss.Width(title)
	spacerW := m.width - tabsW - titleW - 2
	if spacerW < 1 {
		spacerW = 1
	}
	spacer := strings.Repeat(" ", spacerW)

	line := " " + tabs + spacer + title

	// Horizontal rule below
	rule := lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", m.width))

	return line + "\n" + rule
}

// renderFooter builds the [key→action] hint bar.
func (m AppModel) renderFooter() string {
	var hints [][2]string

	if m.activeTab == tabListener && m.listener.insertMode {
		if m.listener.formField == 0 {
			hints = [][2]string{
				{"h/l", "cycle interface"},
				{"Tab", "next field"},
				{"Esc", "cancel"},
			}
		} else {
			hints = [][2]string{
				{"Tab", "prev field"},
				{"Enter", "start listener"},
				{"Esc", "cancel"},
			}
		}
	} else if m.activeTab == tabListener {
		hints = [][2]string{
			{"i", "new listener"},
			{"j/k", "navigate"},
			{"d", "delete listener"},
			{"Tab", "switch tab"},
			{"q", "quit"},
		}
	} else if m.activeTab == tabPayload {
		hints = [][2]string{
			{"j/k", "select payload"},
			{"h/l", "cycle listener"},
			{"Enter", "copy to clipboard"},
			{"Tab", "switch tab"},
			{"q", "quit"},
		}
	} else {
		hints = [][2]string{
			{"j/k", "navigate"},
			{"Enter", "action menu"},
			{"d", "remove client"},
			{"Tab", "cycle"},
			{"q", "quit"},
		}
	}

	return statusBar(m.width, hints)
}
