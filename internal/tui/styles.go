package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Palette ───────────────────────────────────────────────────────────────────

var (
	colorBorder  = lipgloss.Color("#3C3C3C")
	colorFocused = lipgloss.Color("#4FC1FF")
	colorTitle   = lipgloss.Color("#D4D4D4")
	colorText    = lipgloss.Color("#9CDCFE")
	colorMuted   = lipgloss.Color("#6A6A6A")
	colorSuccess = lipgloss.Color("#4EC994")
	colorError   = lipgloss.Color("#F44747")
	colorWarning = lipgloss.Color("#CE9178")
	colorDim     = lipgloss.Color("#4D4D4D")
)

// ── Text styles ───────────────────────────────────────────────────────────────

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(colorTitle).
			Bold(true)

	textStyle = lipgloss.NewStyle().
			Foreground(colorText)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	successStyle = lipgloss.NewStyle().
			Foreground(colorSuccess)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)

	warningStyle = lipgloss.NewStyle().
			Foreground(colorWarning)

	// Status bar key hint styles — matches the [k→action] reference design.
	keyStyle = lipgloss.NewStyle().
			Foreground(colorTitle).
			Bold(true)

	keyDescStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Focused input label
	focusedLabelStyle = lipgloss.NewStyle().
				Foreground(colorFocused)

	// Normal input value
	valueStyle = lipgloss.NewStyle().
			Foreground(colorTitle)

	// Mode badge
	insertBadgeStyle = lipgloss.NewStyle().
				Foreground(colorFocused).
				Bold(true)

	normalBadgeStyle = lipgloss.NewStyle().
				Foreground(colorMuted)
)

// ── Pane helper ───────────────────────────────────────────────────────────────
//
// pane draws a titled boxed pane:
//
//	┌─ Title ───────────────────────────────────┐
//	│ content                                   │
//	└───────────────────────────────────────────┘
func pane(title, content string, w int, focused bool) string {
	bc := colorBorder
	if focused {
		bc = colorFocused
	}

	b := func(s string) string {
		return lipgloss.NewStyle().Foreground(bc).Render(s)
	}

	innerW := w - 2
	if innerW < 2 {
		innerW = 2
	}

	// Top border with title embedded.
	titleFmt := " " + title + " "
	titleRendered := lipgloss.NewStyle().
		Foreground(colorTitle).
		Bold(true).
		Render(titleFmt)

	leftDash := 1
	rightDash := innerW - leftDash - lipgloss.Width(titleFmt)
	if rightDash < 0 {
		rightDash = 0
	}
	top := b("┌") + b(strings.Repeat("─", leftDash)) +
		titleRendered +
		b(strings.Repeat("─", rightDash)) + b("┐")

	// Content lines.
	lines := strings.Split(content, "\n")
	var rows []string
	rows = append(rows, top)
	for _, line := range lines {
		lw := lipgloss.Width(line)
		pad := innerW - lw
		if pad < 0 {
			// Clamp to innerW — ANSI-aware truncation prevents border overflow.
			line = lipgloss.NewStyle().MaxWidth(innerW).Render(line)
			pad = 0
		}
		rows = append(rows, b("│")+line+strings.Repeat(" ", pad)+b("│"))
	}

	// Bottom border.
	bot := b("└") + b(strings.Repeat("─", innerW)) + b("┘")
	rows = append(rows, bot)

	return strings.Join(rows, "\n")
}

// statusBar renders the bottom keybinding bar.
// hints should be pairs of [key, description].
func statusBar(w int, hints [][2]string) string {
	var parts []string
	for _, h := range hints {
		chunk := lipgloss.NewStyle().Foreground(colorDim).Render("[") +
			keyStyle.Render(h[0]) +
			lipgloss.NewStyle().Foreground(colorDim).Render("→") +
			keyDescStyle.Render(h[1]) +
			lipgloss.NewStyle().Foreground(colorDim).Render("]")
		parts = append(parts, chunk)
	}
	bar := strings.Join(parts, " ")
	return lipgloss.NewStyle().
		Width(w).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(colorBorder).
		Render(" " + bar)
}
