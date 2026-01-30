package tui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/processor"
)

func TestNewModel(t *testing.T) {
	tests := []struct {
		name  string
		state State
	}{
		{name: "executing state", state: StateExecuting},
		{name: "select plan state", state: StateSelectPlan},
		{name: "create plan state", state: StateCreatePlan},
		{name: "done state", state: StateDone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(tc.state)
			assert.Equal(t, tc.state, m.state)
			assert.NotNil(t, m.styles)
			assert.Empty(t, m.output)
			assert.NoError(t, m.err)
		})
	}
}

func TestNewModel_WithPlansDir(t *testing.T) {
	m := NewModel(StateSelectPlan).WithPlansDir("/tmp/plans")
	assert.Equal(t, StateSelectPlan, m.state)
	assert.Equal(t, "/tmp/plans", m.plansDir)
	assert.NotNil(t, m.styles)
}

func TestModel_Init(t *testing.T) {
	m := NewModel(StateExecuting)
	cmd := m.Init()
	assert.Nil(t, cmd)
}

func TestModel_Init_SelectPlanWithDir(t *testing.T) {
	m := NewModel(StateSelectPlan).WithPlansDir("/tmp/plans")
	cmd := m.Init()
	assert.NotNil(t, cmd, "Init should return a command when state is SelectPlan with plansDir")
}

func TestModel_Init_SelectPlanWithoutDir(t *testing.T) {
	m := NewModel(StateSelectPlan)
	cmd := m.Init()
	assert.Nil(t, cmd, "Init should return nil when plansDir is empty")
}

func TestModel_Update_Quit(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "q key", key: "q"},
		{name: "ctrl+c", key: "ctrl+c"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(StateExecuting)
			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
			_ = updated
			// for ctrl+c, bubbletea uses a special key type
			if tc.key == "ctrl+c" {
				m2 := NewModel(StateExecuting)
				updated2, cmd2 := m2.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
				_ = updated2
				assert.NotNil(t, cmd2, "ctrl+c should produce quit command")
			} else {
				assert.NotNil(t, cmd, "q should produce quit command")
			}
		})
	}
}

func TestModel_Update_WindowSize(t *testing.T) {
	m := NewModel(StateExecuting)
	assert.False(t, m.ready)

	// first window size message initializes viewport
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	assert.True(t, m.ready)
	assert.Equal(t, 80, m.width)
	assert.Equal(t, 24, m.height)

	// second window size message resizes viewport
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)
	assert.Equal(t, 120, m.width)
	assert.Equal(t, 40, m.height)
}

func TestModel_Update_OutputMsg(t *testing.T) {
	m := NewModel(StateExecuting)
	// initialize viewport first
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(OutputMsg{Text: "line 1"})
	m = updated.(Model)
	assert.Len(t, m.output, 1)
	assert.Equal(t, "line 1", m.output[0])

	updated, _ = m.Update(OutputMsg{Text: "line 2"})
	m = updated.(Model)
	assert.Len(t, m.output, 2)
	assert.Equal(t, "line 2", m.output[1])
}

func TestModel_appendOutput_capsAtMaxLines(t *testing.T) {
	m := NewModel(StateExecuting)
	// initialize viewport
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// add more lines than maxOutputLines directly via appendOutput
	total := maxOutputLines + 100
	for i := range total {
		m.appendOutput(fmt.Sprintf("line-%d", i))
	}

	assert.Len(t, m.output, maxOutputLines, "output should be capped at maxOutputLines")
	// oldest lines should be trimmed, first remaining line is line-100
	assert.Equal(t, "line-100", m.output[0])
	assert.Equal(t, fmt.Sprintf("line-%d", total-1), m.output[len(m.output)-1])
}

func TestModel_Update_SectionMsg(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	section := processor.NewTaskIterationSection(1)
	updated, _ = m.Update(SectionMsg{Section: section})
	m = updated.(Model)

	require.Len(t, m.output, 1)
	assert.Contains(t, m.output[0], "task iteration 1")
}

func TestModel_Update_PhaseChangeMsg(t *testing.T) {
	m := NewModel(StateExecuting)

	updated, _ := m.Update(PhaseChangeMsg{Phase: processor.PhaseReview})
	m = updated.(Model)
	assert.Equal(t, processor.PhaseReview, m.phase)

	updated, _ = m.Update(PhaseChangeMsg{Phase: processor.PhaseCodex})
	m = updated.(Model)
	assert.Equal(t, processor.PhaseCodex, m.phase)
}

func TestModel_Update_ExecutionDoneMsg(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "success", err: nil, wantErr: false},
		{name: "with error", err: errors.New("failed"), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(StateExecuting)
			updated, _ := m.Update(ExecutionDoneMsg{Err: tc.err})
			m = updated.(Model)
			assert.Equal(t, StateDone, m.state)
			if tc.wantErr {
				assert.Error(t, m.err)
			} else {
				assert.NoError(t, m.err)
			}
		})
	}
}

func TestModel_Update_ErrorMsg(t *testing.T) {
	m := NewModel(StateExecuting)
	testErr := errors.New("something went wrong")
	updated, _ := m.Update(ErrorMsg{Err: testErr})
	m = updated.(Model)
	assert.Equal(t, testErr, m.err)
}

func TestModel_View_NotReady(t *testing.T) {
	m := NewModel(StateExecuting)
	view := m.View()
	assert.Equal(t, "initializing...", view)
}

func TestModel_View_States(t *testing.T) {
	tests := []struct {
		name     string
		state    State
		contains string
	}{
		{name: "executing", state: StateExecuting, contains: "executing"},
		{name: "select plan", state: StateSelectPlan, contains: "loading plans"},
		{name: "create plan", state: StateCreatePlan, contains: "initializing editor"},
		{name: "question", state: StateQuestion, contains: "navigate"},
		{name: "done", state: StateDone, contains: "done"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(tc.state)
			// initialize viewport
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			m = updated.(Model)

			view := m.View()
			assert.Contains(t, view, tc.contains)
		})
	}
}

func TestModel_View_DoneWithError(t *testing.T) {
	m := NewModel(StateDone)
	m.err = errors.New("test error")

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "test error")
}

func TestModel_View_WithPhase(t *testing.T) {
	m := NewModel(StateExecuting)
	m.phase = processor.PhaseTask

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "ralphex")
	assert.Contains(t, view, "task")
}

// test message types

func TestOutputMsg(t *testing.T) {
	msg := OutputMsg{Text: "hello"}
	assert.Equal(t, "hello", msg.Text)
}

func TestSectionMsg(t *testing.T) {
	section := processor.NewGenericSection("test section")
	msg := SectionMsg{Section: section}
	assert.Equal(t, "test section", msg.Section.Label)
	assert.Equal(t, processor.SectionGeneric, msg.Section.Type)
}

func TestPhaseChangeMsg(t *testing.T) {
	msg := PhaseChangeMsg{Phase: processor.PhaseReview}
	assert.Equal(t, processor.PhaseReview, msg.Phase)
}

func TestQuestionMsg(t *testing.T) {
	msg := QuestionMsg{
		Question: "which option?",
		Options:  []string{"a", "b", "c"},
	}
	assert.Equal(t, "which option?", msg.Question)
	assert.Equal(t, []string{"a", "b", "c"}, msg.Options)
}

func TestExecutionDoneMsg(t *testing.T) {
	msg := ExecutionDoneMsg{Err: nil}
	require.NoError(t, msg.Err)

	msg = ExecutionDoneMsg{Err: errors.New("fail")}
	assert.Error(t, msg.Err)
}

func TestErrorMsg(t *testing.T) {
	msg := ErrorMsg{Err: errors.New("oops")}
	assert.EqualError(t, msg.Err, "oops")
}

func TestPlanSelectedMsg(t *testing.T) {
	msg := PlanSelectedMsg{Path: "/path/to/plan.md"}
	assert.Equal(t, "/path/to/plan.md", msg.Path)
}

func TestPlanCreatedMsg(t *testing.T) {
	msg := PlanCreatedMsg{Description: "new feature"}
	assert.Equal(t, "new feature", msg.Description)
}

// test styles

func TestNewStyles(t *testing.T) {
	s := NewStyles()
	assert.NotNil(t, s)

	// verify all styles are initialized (non-zero value)
	assert.NotEmpty(t, s.Task.GetForeground())
	assert.NotEmpty(t, s.Review.GetForeground())
	assert.NotEmpty(t, s.Codex.GetForeground())
	assert.NotEmpty(t, s.ClaudeEval.GetForeground())
	assert.NotEmpty(t, s.Warn.GetForeground())
	assert.NotEmpty(t, s.Error.GetForeground())
	assert.NotEmpty(t, s.Signal.GetForeground())
	assert.NotEmpty(t, s.Timestamp.GetForeground())
	assert.NotEmpty(t, s.Info.GetForeground())
	assert.NotEmpty(t, s.DialogBorder.GetBorderStyle())
	assert.NotEmpty(t, s.DialogTitle.GetForeground())
	assert.NotEmpty(t, s.HotkeyKey.GetForeground())
	assert.NotEmpty(t, s.HotkeyDesc.GetForeground())
}

// plan selection tests

func TestModel_Update_InitPlanListMsg(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	items := []list.Item{
		planItem{path: "/a.md", name: "plan-a", desc: "Plan A"},
		planItem{path: "/b.md", name: "plan-b", desc: "Plan B"},
	}

	updated, _ = m.Update(initPlanListMsg{items: items})
	m = updated.(Model)

	assert.True(t, m.planReady)
	assert.False(t, m.emptyList)
	assert.Len(t, m.planList.Items(), 2)
}

func TestModel_Update_InitPlanListMsg_Empty(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(initPlanListMsg{items: nil})
	m = updated.(Model)

	assert.True(t, m.planReady)
	assert.True(t, m.emptyList)
}

func TestModel_Update_InitPlanListMsg_Error(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(initPlanListMsg{err: errors.New("read failed")})
	m = updated.(Model)

	require.Error(t, m.err)
	assert.False(t, m.planReady)
}

func TestModel_Update_PlanListSelection(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	items := []list.Item{
		planItem{path: "/plans/feature.md", name: "feature", desc: "Feature plan"},
	}
	updated, _ = m.Update(initPlanListMsg{items: items})
	m = updated.(Model)

	// press enter to select
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	require.NotNil(t, cmd)
	msg := cmd()
	selected, ok := msg.(PlanSelectedMsg)
	require.True(t, ok)
	assert.Equal(t, "/plans/feature.md", selected.Path)
}

func TestModel_Update_PlanListQuit(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	items := []list.Item{
		planItem{path: "/a.md", name: "plan-a", desc: "A"},
	}
	updated, _ = m.Update(initPlanListMsg{items: items})
	m = updated.(Model)

	// ctrl+c should quit
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.NotNil(t, cmd, "ctrl+c in plan list should quit")
}

func TestModel_Update_PlanListNewPlan(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	items := []list.Item{
		planItem{path: "/a.md", name: "plan-a", desc: "A"},
	}
	updated, _ = m.Update(initPlanListMsg{items: items})
	m = updated.(Model)

	// "n" should switch to create plan state
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(Model)
	assert.Equal(t, StateCreatePlan, m.state)
}

func TestModel_Update_EmptyListNewPlan(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(initPlanListMsg{items: nil})
	m = updated.(Model)
	assert.True(t, m.emptyList)

	// "n" should switch to create plan state
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(Model)
	assert.Equal(t, StateCreatePlan, m.state)
}

func TestModel_Update_EmptyListQuit(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(initPlanListMsg{items: nil})
	m = updated.(Model)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	assert.NotNil(t, cmd, "q on empty list should quit")
}

func TestModel_View_PlanListLoading(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "loading plans")
}

func TestModel_View_PlanListEmpty(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(initPlanListMsg{items: nil})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "No plans found")
	assert.Contains(t, view, "Plan Selection")
}

func TestModel_View_PlanListWithItems(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	items := []list.Item{
		planItem{path: "/a.md", name: "plan-alpha", desc: "Alpha plan"},
		planItem{path: "/b.md", name: "plan-beta", desc: "Beta plan"},
	}
	updated, _ = m.Update(initPlanListMsg{items: items})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "select plan")
	assert.Contains(t, view, "plan-alpha")
}

func TestModel_Update_WindowSize_ResizesPlanList(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	items := []list.Item{
		planItem{path: "/a.md", name: "plan-a", desc: "A"},
	}
	updated, _ = m.Update(initPlanListMsg{items: items})
	m = updated.(Model)

	// resize
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	assert.Equal(t, 120, m.width)
	assert.Equal(t, 40, m.height)
}

func TestStatusLabel(t *testing.T) {
	tests := []struct {
		name     string
		state    State
		phase    string
		expected string
	}{
		{name: "executing no phase", state: StateExecuting, phase: "", expected: "executing"},
		{name: "executing with phase", state: StateExecuting, phase: "task", expected: "task"},
		{name: "done", state: StateDone, expected: "done"},
		{name: "question", state: StateQuestion, expected: "question"},
		{name: "select plan", state: StateSelectPlan, expected: ""},
		{name: "create plan", state: StateCreatePlan, expected: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := statusLabel(tc.state, tc.phase)
			assert.Equal(t, tc.expected, result)
		})
	}
}
