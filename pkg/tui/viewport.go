package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

// headerHeight is the number of lines reserved for the top header bar.
const headerHeight = 1

// statusBarHeight is the number of lines reserved for the bottom status bar and hotkey hints.
const statusBarHeight = 2

// reservedHeight is the total number of lines reserved for header and status bar.
const reservedHeight = headerHeight + statusBarHeight

// viewportConfig holds the configuration for the execution viewport.
type viewportConfig struct {
	version  string // ralphex version/revision
	branch   string // current git branch name
	planName string // name of the plan being executed
}

// NewViewportConfig creates a viewport configuration for the TUI header and status bar.
func NewViewportConfig(version, branch, planName string) viewportConfig {
	return viewportConfig{version: version, branch: branch, planName: planName}
}

// newViewport creates a configured viewport for execution output.
func newViewport(width, height int) viewport.Model {
	vp := viewport.New(width, contentHeight(height))
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 3
	return vp
}

// contentHeight calculates the available height for the viewport content area.
func contentHeight(totalHeight int) int {
	return max(totalHeight-reservedHeight, 1)
}

// renderHeader renders the top header bar with version and branch info.
func renderHeader(styles *Styles, cfg viewportConfig, phase string, width int) string {
	left := styles.Task.Bold(true).Render("ralphex")
	if cfg.version != "" && cfg.version != "unknown" {
		left += " " + styles.Timestamp.Render(cfg.version)
	}
	if cfg.branch != "" {
		left += " " + styles.Info.Render("on "+cfg.branch)
	}
	if phase != "" {
		left += " " + styles.Info.Render("["+phase+"]")
	}

	// pad header to full width with background
	headerStyle := lipgloss.NewStyle().
		Width(width).
		Background(lipgloss.Color("#1a1a2e"))
	return headerStyle.Render(left)
}

// renderStatusBar renders the bottom status bar with phase, elapsed time, and plan name.
func renderStatusBar(styles *Styles, cfg viewportConfig, state State, phase string, elapsed time.Duration,
	autoScroll bool, scrollPct float64, err error) string {
	var parts []string

	// state/phase indicator
	stateStr := statusLabel(state, phase)
	if stateStr != "" {
		parts = append(parts, stateStr)
	}

	// plan name
	if cfg.planName != "" {
		parts = append(parts, cfg.planName)
	}

	// elapsed time
	parts = append(parts, formatElapsed(elapsed))

	// scroll indicator
	if !autoScroll {
		parts = append(parts, fmt.Sprintf("scroll: %.0f%%", scrollPct*100))
	}

	// error indicator
	if err != nil {
		parts = append(parts, styles.Error.Render("error: "+err.Error()))
	}

	status := " " + strings.Join(parts, " | ")

	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#b4b4b4")).
		Background(lipgloss.Color("#333333")).
		Width(0) // we do not want lipgloss to pad; the caller does full-width padding
	return statusStyle.Render(status)
}

// statusLabel returns a short label for the current state/phase combination.
func statusLabel(state State, phase string) string {
	switch state {
	case StateExecuting:
		if phase != "" {
			return phase
		}
		return "executing"
	case StateDone:
		return "done"
	case StateQuestion:
		return "question"
	default:
		return ""
	}
}

// formatElapsed formats a duration as HH:MM:SS or MM:SS.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// renderHotkeyBar renders context-sensitive hotkey hints for the current state.
func renderHotkeyBar(styles *Styles, state State, width int) string {
	type hint struct {
		key  string
		desc string
	}

	var hints []hint
	switch state {
	case StateExecuting:
		hints = []hint{
			{key: "↑/↓", desc: "scroll"},
			{key: "pgup/pgdn", desc: "page"},
			{key: "home/end", desc: "jump"},
			{key: "q", desc: "quit"},
		}
	case StateQuestion:
		hints = []hint{
			{key: "↑/↓", desc: "navigate"},
			{key: "enter", desc: "select"},
			{key: "ctrl+c", desc: "quit"},
		}
	case StateCreatePlan:
		hints = []hint{
			{key: "ctrl+d", desc: "submit"},
			{key: "esc", desc: "submit"},
			{key: "ctrl+c", desc: "cancel"},
		}
	case StateSelectPlan:
		hints = []hint{
			{key: "enter", desc: "select"},
			{key: "/", desc: "filter"},
			{key: "n", desc: "new plan"},
			{key: "q", desc: "quit"},
		}
	case StateDone:
		hints = []hint{
			{key: "q", desc: "quit"},
		}
	}

	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, styles.HotkeyKey.Render(h.key)+" "+styles.HotkeyDesc.Render(h.desc))
	}

	bar := " " + strings.Join(parts, "  ")
	barStyle := lipgloss.NewStyle().
		Width(width).
		Background(lipgloss.Color("#1a1a2e"))
	return barStyle.Render(bar)
}
