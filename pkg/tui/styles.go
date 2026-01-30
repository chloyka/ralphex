package tui

import "github.com/charmbracelet/lipgloss"

// Styles holds all lipgloss styles for TUI rendering.
// mirrors the color scheme from progress.Colors with hex values from config defaults.
type Styles struct {
	Task       lipgloss.Style
	Review     lipgloss.Style
	Codex      lipgloss.Style
	ClaudeEval lipgloss.Style
	Warn       lipgloss.Style
	Error      lipgloss.Style
	Signal     lipgloss.Style
	Timestamp  lipgloss.Style
	Info       lipgloss.Style

	DialogBorder lipgloss.Style // rounded border for dialog frames
	DialogTitle  lipgloss.Style // bold title for dialogs
	HotkeyKey    lipgloss.Style // bold key name in hotkey hints
	HotkeyDesc   lipgloss.Style // dim description in hotkey hints
}

// NewStyles creates Styles with the default color scheme.
func NewStyles() *Styles {
	s := &Styles{
		Task:       lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")),
		Review:     lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffff")),
		Codex:      lipgloss.NewStyle().Foreground(lipgloss.Color("#d096d9")),
		ClaudeEval: lipgloss.NewStyle().Foreground(lipgloss.Color("#bdd6ff")),
		Warn:       lipgloss.NewStyle().Foreground(lipgloss.Color("#ffc56d")),
		Error:      lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")),
		Signal:     lipgloss.NewStyle().Foreground(lipgloss.Color("#d25252")),
		Timestamp:  lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8a8a")),
		Info:       lipgloss.NewStyle().Foreground(lipgloss.Color("#b4b4b4")),

		DialogBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#555555")),
		DialogTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00ff00")).
			Bold(true),
		HotkeyKey: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00ffff")).
			Bold(true),
		HotkeyDesc: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8a8a8a")),
	}

	return s
}
