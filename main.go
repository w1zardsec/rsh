package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"codeberg.org/asuna/rsh/internal/tui"
)

func main() {
	app := tui.NewApp()

	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),       // full-screen alternate buffer
		tea.WithMouseCellMotion(), // mouse support (optional, nice to have)
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "rsh: %v\n", err)
		os.Exit(1)
	}
}
