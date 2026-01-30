package tui

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/processor"
)

func TestContentHeight(t *testing.T) {
	tests := []struct {
		name        string
		totalHeight int
		expected    int
	}{
		{name: "normal terminal", totalHeight: 24, expected: 21},
		{name: "large terminal", totalHeight: 50, expected: 47},
		{name: "minimum height", totalHeight: 3, expected: 1},
		{name: "below minimum", totalHeight: 1, expected: 1},
		{name: "zero height", totalHeight: 0, expected: 1},
		{name: "negative height", totalHeight: -5, expected: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := contentHeight(tc.totalHeight)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestNewViewport(t *testing.T) {
	vp := newViewport(80, 24)
	assert.Equal(t, 80, vp.Width)
	assert.Equal(t, contentHeight(24), vp.Height)
	assert.True(t, vp.MouseWheelEnabled)
	assert.Equal(t, 3, vp.MouseWheelDelta)
}

func TestRenderHeader(t *testing.T) {
	styles := NewStyles()

	tests := []struct {
		name     string
		cfg      viewportConfig
		phase    string
		contains []string
	}{
		{
			name:     "basic header",
			cfg:      viewportConfig{},
			phase:    "",
			contains: []string{"ralphex"},
		},
		{
			name:     "with version and branch",
			cfg:      viewportConfig{version: "v1.2.3", branch: "feature-test"},
			phase:    "",
			contains: []string{"ralphex", "v1.2.3", "feature-test"},
		},
		{
			name:     "with phase",
			cfg:      viewportConfig{},
			phase:    "task",
			contains: []string{"ralphex", "task"},
		},
		{
			name:     "version unknown omitted",
			cfg:      viewportConfig{version: "unknown"},
			phase:    "",
			contains: []string{"ralphex"},
		},
		{
			name:     "full header",
			cfg:      viewportConfig{version: "abc1234", branch: "main"},
			phase:    "review",
			contains: []string{"ralphex", "abc1234", "main", "review"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderHeader(styles, tc.cfg, tc.phase, 80)
			for _, s := range tc.contains {
				assert.Contains(t, result, s)
			}
		})
	}
}

func TestRenderHeader_UnknownVersionOmitted(t *testing.T) {
	styles := NewStyles()
	result := renderHeader(styles, viewportConfig{version: "unknown"}, "", 80)
	assert.NotContains(t, result, "unknown")
}

func TestRenderStatusBar(t *testing.T) {
	styles := NewStyles()

	tests := []struct {
		name       string
		cfg        viewportConfig
		state      State
		phase      string
		elapsed    time.Duration
		autoScroll bool
		scrollPct  float64
		err        error
		contains   []string
		excludes   []string
	}{
		{
			name:       "executing with plan",
			cfg:        viewportConfig{planName: "my-plan"},
			state:      StateExecuting,
			phase:      "task",
			elapsed:    2*time.Minute + 30*time.Second,
			autoScroll: true,
			contains:   []string{"task", "my-plan", "2:30"},
			excludes:   []string{"scroll"},
		},
		{
			name:       "scrolled up shows scroll percentage",
			cfg:        viewportConfig{},
			state:      StateExecuting,
			phase:      "review",
			elapsed:    5 * time.Second,
			autoScroll: false,
			scrollPct:  0.5,
			contains:   []string{"scroll: 50%"},
		},
		{
			name:       "done with error",
			cfg:        viewportConfig{},
			state:      StateDone,
			phase:      "",
			elapsed:    10 * time.Minute,
			autoScroll: true,
			err:        errors.New("something failed"),
			contains:   []string{"done", "10:00", "something failed"},
		},
		{
			name:       "hour-long elapsed time",
			cfg:        viewportConfig{},
			state:      StateExecuting,
			phase:      "",
			elapsed:    1*time.Hour + 5*time.Minute + 3*time.Second,
			autoScroll: true,
			contains:   []string{"1:05:03"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderStatusBar(styles, tc.cfg, tc.state, tc.phase, tc.elapsed,
				tc.autoScroll, tc.scrollPct, tc.err)
			for _, s := range tc.contains {
				assert.Contains(t, result, s)
			}
			for _, s := range tc.excludes {
				assert.NotContains(t, result, s)
			}
		})
	}
}

func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{name: "zero", duration: 0, expected: "0:00"},
		{name: "seconds only", duration: 45 * time.Second, expected: "0:45"},
		{name: "minutes and seconds", duration: 3*time.Minute + 15*time.Second, expected: "3:15"},
		{name: "hours", duration: 2*time.Hour + 30*time.Minute + 5*time.Second, expected: "2:30:05"},
		{name: "exact minute", duration: 1 * time.Minute, expected: "1:00"},
		{name: "exact hour", duration: 1 * time.Hour, expected: "1:00:00"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := formatElapsed(tc.duration)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// viewport integration tests with the Model

func TestModel_ViewportAutoScroll(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// auto-scroll should be enabled by default
	assert.True(t, m.autoScroll)

	// add output - should auto-scroll
	for range 30 {
		updated, _ = m.Update(OutputMsg{Text: "line"})
		m = updated.(Model)
	}
	assert.True(t, m.autoScroll)
	assert.True(t, m.viewport.AtBottom())
}

func TestModel_ViewportScrollUpDisablesAutoScroll(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updated.(Model)

	// add enough lines to make viewport scrollable
	for range 30 {
		updated, _ = m.Update(OutputMsg{Text: "line"})
		m = updated.(Model)
	}

	// scroll up should disable auto-scroll
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.False(t, m.autoScroll)
}

func TestModel_ViewportScrollDownReEnablesAutoScroll(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updated.(Model)

	// add enough content
	for range 30 {
		updated, _ = m.Update(OutputMsg{Text: "line"})
		m = updated.(Model)
	}

	// scroll up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.False(t, m.autoScroll)

	// scroll to end re-enables auto-scroll
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("end")})
	m = updated.(Model)
	assert.True(t, m.autoScroll)
}

func TestModel_ViewportPageUpDown(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updated.(Model)

	// add enough content
	for range 50 {
		updated, _ = m.Update(OutputMsg{Text: "line"})
		m = updated.(Model)
	}

	// page up disables auto-scroll
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(Model)
	assert.False(t, m.autoScroll)
}

func TestModel_ViewportHomeEnd(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updated.(Model)

	for range 30 {
		updated, _ = m.Update(OutputMsg{Text: "line"})
		m = updated.(Model)
	}

	// home goes to top, disables auto-scroll
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = updated.(Model)
	assert.False(t, m.autoScroll)
	assert.True(t, m.viewport.AtTop())

	// end goes to bottom, re-enables auto-scroll
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	assert.True(t, m.autoScroll)
	assert.True(t, m.viewport.AtBottom())
}

func TestModel_ViewportNoAutoScrollWhenScrolledUp(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updated.(Model)

	for range 30 {
		updated, _ = m.Update(OutputMsg{Text: "line"})
		m = updated.(Model)
	}

	// scroll up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.False(t, m.autoScroll)

	// new output should not scroll to bottom when auto-scroll is off
	yOffBefore := m.viewport.YOffset
	updated, _ = m.Update(OutputMsg{Text: "new output"})
	m = updated.(Model)
	assert.False(t, m.autoScroll)
	assert.Equal(t, yOffBefore, m.viewport.YOffset)
}

func TestModel_ViewportResizeAdjustsDimensions(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	assert.Equal(t, 80, m.viewport.Width)
	assert.Equal(t, contentHeight(24), m.viewport.Height)

	// resize to larger
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)
	assert.Equal(t, 120, m.viewport.Width)
	assert.Equal(t, contentHeight(40), m.viewport.Height)

	// resize to smaller
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 15})
	m = updated.(Model)
	assert.Equal(t, 60, m.viewport.Width)
	assert.Equal(t, contentHeight(15), m.viewport.Height)
}

func TestModel_ViewportContentAccumulation(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// add multiple output messages
	updated, _ = m.Update(OutputMsg{Text: "first line"})
	m = updated.(Model)
	require.Len(t, m.output, 1)

	updated, _ = m.Update(OutputMsg{Text: "second line"})
	m = updated.(Model)
	require.Len(t, m.output, 2)

	// add section message
	section := processor.NewTaskIterationSection(1)
	updated, _ = m.Update(SectionMsg{Section: section})
	m = updated.(Model)
	require.Len(t, m.output, 3)
	assert.Contains(t, m.output[2], "task iteration 1")

	// add more output
	updated, _ = m.Update(OutputMsg{Text: "third line"})
	m = updated.(Model)
	require.Len(t, m.output, 4)
}

func TestModel_ViewportVimKeys(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updated.(Model)

	for range 30 {
		updated, _ = m.Update(OutputMsg{Text: "line"})
		m = updated.(Model)
	}

	// k scrolls up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = updated.(Model)
	assert.False(t, m.autoScroll)

	// j scrolls down
	for range 100 { // scroll all the way down
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = updated.(Model)
	}
	assert.True(t, m.autoScroll)
}

func TestModel_NewModelWithConfig(t *testing.T) {
	cfg := viewportConfig{
		version:  "v1.0.0",
		branch:   "main",
		planName: "test-plan",
	}
	m := NewModelWithConfig(StateExecuting, cfg)
	assert.Equal(t, StateExecuting, m.state)
	assert.Equal(t, "v1.0.0", m.vpConfig.version)
	assert.Equal(t, "main", m.vpConfig.branch)
	assert.Equal(t, "test-plan", m.vpConfig.planName)
	assert.True(t, m.autoScroll)
	assert.NotNil(t, m.styles)
}

func TestModel_ViewHeader(t *testing.T) {
	cfg := viewportConfig{version: "abc123", branch: "feature"}
	m := NewModelWithConfig(StateExecuting, cfg)
	m.phase = processor.PhaseTask

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "ralphex")
	assert.Contains(t, view, "abc123")
	assert.Contains(t, view, "feature")
	assert.Contains(t, view, "task")
}

func TestModel_ViewStatusBar(t *testing.T) {
	cfg := viewportConfig{planName: "test-plan"}
	m := NewModelWithConfig(StateExecuting, cfg)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "test-plan")
	assert.Contains(t, view, "executing")
}

func TestModel_ViewportQuitDuringExecution(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// ctrl+c should quit
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.NotNil(t, cmd, "ctrl+c during execution should quit")
}

func TestModel_ViewportQuitDuringDone(t *testing.T) {
	m := NewModel(StateDone)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// q should quit when done
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	assert.NotNil(t, cmd, "q when done should quit")
}

func TestModel_SectionMsgAutoScroll(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updated.(Model)

	for range 30 {
		updated, _ = m.Update(OutputMsg{Text: "line"})
		m = updated.(Model)
	}

	// scroll up to disable auto-scroll
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.False(t, m.autoScroll)

	// section msg should not auto-scroll when disabled
	yOffBefore := m.viewport.YOffset
	section := processor.NewGenericSection("test section")
	updated, _ = m.Update(SectionMsg{Section: section})
	m = updated.(Model)
	assert.Equal(t, yOffBefore, m.viewport.YOffset)
}

func TestRenderHotkeyBar(t *testing.T) {
	styles := NewStyles()

	tests := []struct {
		name     string
		state    State
		contains []string
	}{
		{
			name:     "executing state",
			state:    StateExecuting,
			contains: []string{"scroll", "page", "jump", "quit"},
		},
		{
			name:     "question state",
			state:    StateQuestion,
			contains: []string{"navigate", "select", "quit"},
		},
		{
			name:     "create plan state",
			state:    StateCreatePlan,
			contains: []string{"submit", "cancel"},
		},
		{
			name:     "select plan state",
			state:    StateSelectPlan,
			contains: []string{"select", "filter", "new plan", "quit"},
		},
		{
			name:     "done state",
			state:    StateDone,
			contains: []string{"quit"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderHotkeyBar(styles, tc.state, 80)
			for _, s := range tc.contains {
				assert.Contains(t, result, s)
			}
		})
	}
}
