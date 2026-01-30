package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPlanCreate(t *testing.T) {
	pc := newPlanCreate(80, 24)
	assert.NotEmpty(t, pc.textarea.Placeholder)
	assert.True(t, pc.textarea.Focused())
}

func TestNewPlanCreate_SmallDimensions(t *testing.T) {
	pc := newPlanCreate(2, 4)
	// should not panic with small dimensions
	assert.NotEmpty(t, pc.textarea.Placeholder)
}

func TestPlanCreateModel_View(t *testing.T) {
	pc := newPlanCreate(80, 24)
	styles := NewStyles()
	view := pc.view(styles, 80)

	assert.Contains(t, view, "Create New Plan")
	assert.Contains(t, view, "Describe what you want to build")
	assert.Contains(t, view, "clarifying questions")
}

func TestPlanCreateModel_SubmitCtrlD(t *testing.T) {
	pc := newPlanCreate(80, 24)

	// type some text
	pc.textarea.SetValue("my plan description")

	// press Ctrl+D to submit
	updated, cmd := pc.updatePlanCreate(tea.KeyMsg{Type: tea.KeyCtrlD})
	_ = updated

	require.NotNil(t, cmd)
	msg := cmd()
	submitMsg, ok := msg.(planCreateSubmitMsg)
	require.True(t, ok)
	assert.Equal(t, "my plan description", submitMsg.description)
}

func TestPlanCreateModel_SubmitEsc(t *testing.T) {
	pc := newPlanCreate(80, 24)

	// type some text
	pc.textarea.SetValue("escape submit test")

	// press Esc to submit
	updated, cmd := pc.updatePlanCreate(tea.KeyMsg{Type: tea.KeyEsc})
	_ = updated

	require.NotNil(t, cmd)
	msg := cmd()
	submitMsg, ok := msg.(planCreateSubmitMsg)
	require.True(t, ok)
	assert.Equal(t, "escape submit test", submitMsg.description)
}

func TestPlanCreateModel_EscEmptyCancels(t *testing.T) {
	pc := newPlanCreate(80, 24)
	// leave textarea empty

	// Esc with empty text should cancel
	_, cmd := pc.updatePlanCreate(tea.KeyMsg{Type: tea.KeyEsc})

	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(planCreateCancelMsg)
	assert.True(t, ok, "Esc with empty text should produce planCreateCancelMsg")
}

func TestPlanCreateModel_CtrlDEmptyNoOp(t *testing.T) {
	pc := newPlanCreate(80, 24)
	// leave textarea empty

	// Ctrl+D with empty text should not submit (no-op)
	_, cmd := pc.updatePlanCreate(tea.KeyMsg{Type: tea.KeyCtrlD})

	assert.Nil(t, cmd, "Ctrl+D with empty text should not submit")
}

func TestPlanCreateModel_CancelCtrlC(t *testing.T) {
	pc := newPlanCreate(80, 24)
	pc.textarea.SetValue("some text")

	// press Ctrl+C to cancel
	_, cmd := pc.updatePlanCreate(tea.KeyMsg{Type: tea.KeyCtrlC})

	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(planCreateCancelMsg)
	assert.True(t, ok, "Ctrl+C should produce planCreateCancelMsg")
}

func TestPlanCreateModel_RegularKeyPassthrough(t *testing.T) {
	pc := newPlanCreate(80, 24)

	// type a regular character - it should be passed to textarea
	updated, _ := pc.updatePlanCreate(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	assert.Contains(t, updated.textarea.Value(), "a")
}

// integration tests with the main Model

func TestModel_InitCreatePlan(t *testing.T) {
	m := NewModel(StateCreatePlan)
	cmd := m.Init()
	require.NotNil(t, cmd, "Init should return a command when state is CreatePlan")

	msg := cmd()
	_, ok := msg.(initPlanCreateMsg)
	assert.True(t, ok, "Init should produce initPlanCreateMsg")
}

func TestModel_Update_InitPlanCreateMsg(t *testing.T) {
	m := NewModel(StateCreatePlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(initPlanCreateMsg{})
	m = updated.(Model)

	assert.True(t, m.planCreateReady)
	assert.True(t, m.planCreate.textarea.Focused())
}

func TestModel_Update_PlanCreateSubmit(t *testing.T) {
	m := NewModel(StateCreatePlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// init textarea
	updated, _ = m.Update(initPlanCreateMsg{})
	m = updated.(Model)

	// type text via setting value directly (simulates user input)
	m.planCreate.textarea.SetValue("my feature plan")

	// simulate Ctrl+D submit
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)

	// the planCreateSubmitMsg should be produced
	require.NotNil(t, cmd)
	msg := cmd()
	submitMsg, ok := msg.(planCreateSubmitMsg)
	require.True(t, ok)
	assert.Equal(t, "my feature plan", submitMsg.description)

	// then update with the submit msg - it should produce PlanCreatedMsg
	updated, cmd = m.Update(submitMsg)
	m = updated.(Model)
	require.NotNil(t, cmd)

	finalMsg := cmd()
	created, ok := finalMsg.(PlanCreatedMsg)
	require.True(t, ok)
	assert.Equal(t, "my feature plan", created.Description)
}

func TestModel_Update_PlanCreateCancel(t *testing.T) {
	m := NewModel(StateCreatePlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(initPlanCreateMsg{})
	m = updated.(Model)

	// Ctrl+C should cancel and quit
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(planCreateCancelMsg)
	require.True(t, ok)

	// cancel msg should produce tea.Quit
	_, cmd = m.Update(planCreateCancelMsg{})
	assert.NotNil(t, cmd, "cancel should produce quit command")
}

func TestModel_View_PlanCreate_NotReady(t *testing.T) {
	m := NewModel(StateCreatePlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "initializing editor")
}

func TestModel_View_PlanCreate_Ready(t *testing.T) {
	m := NewModel(StateCreatePlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(initPlanCreateMsg{})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "Create New Plan")
	assert.Contains(t, view, "Describe what you want to build")
}

func TestModel_Update_WindowSize_ResizesPlanCreate(t *testing.T) {
	m := NewModel(StateCreatePlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(initPlanCreateMsg{})
	m = updated.(Model)

	// resize
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	assert.Equal(t, 120, m.width)
	assert.Equal(t, 40, m.height)
}

func TestModel_PlanListNewPlan_InitsTextarea(t *testing.T) {
	m := NewModel(StateSelectPlan)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// load empty plan list
	updated, _ = m.Update(initPlanListMsg{items: nil})
	m = updated.(Model)
	assert.True(t, m.emptyList)

	// press "n" to create new plan
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(Model)

	assert.Equal(t, StateCreatePlan, m.state)
	assert.True(t, m.planCreateReady, "textarea should be initialized after pressing n")
}
