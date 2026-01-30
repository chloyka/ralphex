package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/umputun/ralphex/pkg/processor"
)

// State represents the current state of the TUI.
type State int

const (
	// StateSelectPlan shows the plan selection list.
	StateSelectPlan State = iota
	// StateCreatePlan shows the plan description input.
	StateCreatePlan
	// StateExecuting shows the scrolling log viewport.
	StateExecuting
	// StateQuestion shows an interactive question prompt.
	StateQuestion
	// StateDone shows the final completion status.
	StateDone
)

// Model is the top-level Bubble Tea model for ralphex TUI.
type Model struct {
	viewport viewport.Model
	phase    processor.Phase
	output   []string
	state    State
	err      error
	styles   *Styles
	width    int
	height   int
	ready    bool

	// viewport configuration
	vpConfig   viewportConfig
	autoScroll bool      // true when viewport should auto-scroll to bottom
	startTime  time.Time // start time for elapsed display

	// plan selection state
	planList  list.Model
	plansDir  string // directory to scan for plan files
	planReady bool   // true after plan list items are loaded
	emptyList bool   // true when no plans found

	// plan creation state
	planCreate      planCreateModel
	planCreateReady bool // true after textarea is initialized

	// question state
	question       string
	questionOpts   []string
	questionCursor int
	questionCh     chan string // channel to deliver answer back to collector
	prevState      State       // state to restore after answering

	// plan selection result delivery
	planResultCh chan PlanSelectionResult // channel for delivering plan selection to business logic
}

// NewModel creates a new TUI model with the given initial state.
func NewModel(initialState State) Model {
	return Model{
		state:      initialState,
		styles:     NewStyles(),
		output:     make([]string, 0),
		autoScroll: true,
		startTime:  time.Now(),
	}
}

// NewModelWithConfig creates a TUI model with viewport configuration for header/status display.
func NewModelWithConfig(initialState State, cfg viewportConfig) Model {
	m := NewModel(initialState)
	m.vpConfig = cfg
	return m
}

// WithPlansDir returns a copy of the model with the plans directory set.
func (m Model) WithPlansDir(plansDir string) Model {
	m.plansDir = plansDir
	return m
}

// Result returns the execution error from the model, if any.
func (m Model) Result() error {
	return m.err
}

// Init returns the initial command for the TUI.
func (m Model) Init() tea.Cmd {
	if m.state == StateSelectPlan && m.plansDir != "" {
		return loadPlansCmd(m.plansDir)
	}
	if m.state == StateCreatePlan {
		return func() tea.Msg { return initPlanCreateMsg{} }
	}
	return nil
}

// Update handles incoming messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.updateKey(msg)
	case tea.WindowSizeMsg:
		m.updateWindowSize(msg)
		return m, nil
	case initPlanListMsg:
		return m.updateInitPlanList(msg)
	case initPlanCreateMsg:
		return m.initPlanCreate()
	case planCreateSubmitMsg:
		return m, func() tea.Msg {
			return PlanCreatedMsg{Description: msg.description}
		}
	case planCreateCancelMsg:
		return m, tea.Quit
	default:
		return m.updateMsg(msg)
	}
}

// updateMsg handles non-key, non-return messages that update model state.
func (m Model) updateMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case OutputMsg:
		m.appendOutput(msg.Text)
	case SectionMsg:
		m.appendOutput(m.styles.Warn.Render("=== " + msg.Section.Label + " ==="))
	case PhaseChangeMsg:
		m.phase = msg.Phase
	case QuestionMsg:
		m.handleQuestion(msg)
	case StartupInfoMsg:
		m.vpConfig.planName = msg.PlanFile
		m.vpConfig.branch = msg.Branch
	case ExecutionDoneMsg:
		m.state = StateDone
		m.err = msg.Err
	case ErrorMsg:
		m.err = msg.Err
	case PlanSelectionRequestMsg:
		m.planResultCh = msg.ResultCh
	case PlanSelectedMsg:
		m.deliverPlanResult(PlanSelectionResult{Path: msg.Path})
	case PlanCreatedMsg:
		m.deliverPlanResult(PlanSelectionResult{Description: msg.Description})
	}

	return m.delegateToChild(msg)
}

// delegateToChild forwards messages to child models (plan creation or plan list) if active.
func (m Model) delegateToChild(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.state == StateCreatePlan && m.planCreateReady {
		var cmd tea.Cmd
		m.planCreate, cmd = m.planCreate.updatePlanCreate(msg)
		return m, cmd
	}

	if m.state == StateSelectPlan && m.planReady && !m.emptyList {
		var cmd tea.Cmd
		m.planList, cmd = m.planList.Update(msg)
		return m, cmd
	}

	return m, nil
}

// maxOutputLines is the maximum number of output lines kept in the viewport buffer.
// older lines are trimmed to avoid O(n^2) cost of rebuilding the full content on every append.
const maxOutputLines = 10000

// appendOutput adds a line to the viewport and auto-scrolls if enabled.
func (m *Model) appendOutput(line string) {
	m.output = append(m.output, line)
	if len(m.output) > maxOutputLines {
		m.output = m.output[len(m.output)-maxOutputLines:]
	}
	m.viewport.SetContent(strings.Join(m.output, "\n"))
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

// handleQuestion sets up the question state from a QuestionMsg.
func (m *Model) handleQuestion(msg QuestionMsg) {
	m.prevState = m.state
	m.state = StateQuestion
	m.question = msg.Question
	m.questionOpts = msg.Options
	m.questionCursor = 0
	m.questionCh = msg.answerCh
}

// deliverPlanResult delivers the plan selection result and switches to executing state.
// uses non-blocking send to prevent blocking the TUI event loop on duplicate selections.
func (m *Model) deliverPlanResult(result PlanSelectionResult) {
	if m.planResultCh != nil {
		select {
		case m.planResultCh <- result:
		default: // channel full (duplicate selection), ignore
		}
		m.planResultCh = nil
	}
	m.state = StateExecuting
}

// updateKey dispatches key messages to the appropriate handler based on state.
func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.state == StateQuestion {
		return m.updateQuestion(msg)
	}
	if m.state == StateCreatePlan && m.planCreateReady {
		return m.updatePlanCreateKey(msg)
	}
	if m.state == StateSelectPlan && m.planReady {
		return m.updatePlanList(msg)
	}
	if m.state == StateExecuting || m.state == StateDone {
		return m.updateViewportKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// updateViewportKey handles key events for viewport scrolling during execution/done states.
func (m Model) updateViewportKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.viewport.ScrollUp(1)
		m.autoScroll = false
	case "down", "j":
		m.viewport.ScrollDown(1)
		if m.viewport.AtBottom() {
			m.autoScroll = true
		}
	case "pgup":
		m.viewport.PageUp()
		m.autoScroll = false
	case "pgdown":
		m.viewport.PageDown()
		if m.viewport.AtBottom() {
			m.autoScroll = true
		}
	case "home":
		m.viewport.GotoTop()
		m.autoScroll = false
	case "end":
		m.viewport.GotoBottom()
		m.autoScroll = true
	}
	return m, nil
}

// initPlanCreate initializes the plan creation textarea model.
func (m Model) initPlanCreate() (tea.Model, tea.Cmd) {
	w := m.width
	if w < 1 {
		w = 80
	}
	h := m.height - 2
	if h < 1 {
		h = 20
	}
	m.planCreate = newPlanCreate(w, h)
	m.planCreateReady = true
	return m, m.planCreate.textarea.Focus()
}

// updatePlanCreateKey handles key events when in plan creation state.
func (m Model) updatePlanCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.planCreate, cmd = m.planCreate.updatePlanCreate(msg)
	return m, cmd
}

// updateWindowSize handles terminal resize events.
func (m *Model) updateWindowSize(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height
	ch := contentHeight(msg.Height)
	if !m.ready {
		m.viewport = newViewport(msg.Width, msg.Height)
		m.ready = true
	} else {
		m.viewport.Width = msg.Width
		m.viewport.Height = ch
	}
	if m.planReady && !m.emptyList {
		m.planList.SetSize(msg.Width, ch)
	}
	if m.planCreateReady {
		m.planCreate.textarea.SetWidth(max(msg.Width-10, 1))
		m.planCreate.textarea.SetHeight(max(msg.Height-12, 1))
	}
}

// updateInitPlanList handles the initial plan list loading message.
func (m Model) updateInitPlanList(msg initPlanListMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err
		m.deliverPlanError(msg.err)
		return m, tea.Quit
	}
	if len(msg.items) == 0 {
		m.emptyList = true
		m.planReady = true
		return m, nil
	}
	h := m.height - 2
	if h < 1 {
		h = 20 // sensible default before first WindowSizeMsg
	}
	w := m.width
	if w < 1 {
		w = 80
	}
	m.planList = newPlanList(msg.items, w, h)
	m.planReady = true
	return m, nil
}

// updatePlanList handles key events when in plan selection state.
func (m Model) updatePlanList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// empty list mode: only handle n (new plan) and q/ctrl+c (quit)
	if m.emptyList {
		switch msg.String() {
		case "n":
			m.state = StateCreatePlan
			return m.initPlanCreate()
		case "q", "ctrl+c":
			m.deliverPlanError(ErrNoPlansFound)
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "enter":
		// handle enter for plan selection (only when not filtering)
		if m.planList.FilterState() != list.Filtering {
			if item := m.planList.SelectedItem(); item != nil {
				if pi, ok := item.(planItem); ok {
					return m, func() tea.Msg { return PlanSelectedMsg{Path: pi.path} }
				}
			}
		}
	case "ctrl+c":
		m.deliverPlanError(ErrUserCanceled)
		return m, tea.Quit
	case "n":
		// "n" to create new plan (only when not filtering)
		if m.planList.FilterState() != list.Filtering {
			m.state = StateCreatePlan
			return m.initPlanCreate()
		}
	}

	// delegate to list for all other keys (navigation, filtering, etc.)
	var cmd tea.Cmd
	m.planList, cmd = m.planList.Update(msg)
	return m, cmd
}

// deliverPlanError sends an error to the plan result channel if one is registered.
func (m *Model) deliverPlanError(err error) {
	if m.planResultCh != nil {
		m.planResultCh <- PlanSelectionResult{Err: err}
		m.planResultCh = nil
	}
}

// updateQuestion handles key events when in question state.
func (m Model) updateQuestion(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.questionCursor > 0 {
			m.questionCursor--
		}
	case "down", "j":
		if m.questionCursor < len(m.questionOpts)-1 {
			m.questionCursor++
		}
	case "enter":
		if len(m.questionOpts) > 0 && m.questionCh != nil {
			answer := m.questionOpts[m.questionCursor]
			m.questionCh <- answer
			m.state = m.prevState
			m.question = ""
			m.questionOpts = nil
			m.questionCh = nil
		}
	case "ctrl+c":
		// close question channel to unblock any waiting goroutines
		if m.questionCh != nil {
			close(m.questionCh)
			m.questionCh = nil
		}
		return m, tea.Quit
	}
	return m, nil
}

// View renders the current state of the TUI.
func (m Model) View() string {
	if !m.ready {
		return "initializing..."
	}

	var b strings.Builder

	// header
	phaseStr := string(m.phase)
	b.WriteString(renderHeader(m.styles, m.vpConfig, phaseStr, m.width))
	b.WriteString("\n")

	// main content
	switch m.state {
	case StateExecuting, StateDone:
		b.WriteString(m.viewport.View())
	case StateSelectPlan:
		b.WriteString(m.renderPlanSelect())
	case StateCreatePlan:
		b.WriteString(m.renderPlanCreate())
	case StateQuestion:
		b.WriteString(m.renderQuestion())
	}

	// status bar
	b.WriteString("\n")
	elapsed := time.Since(m.startTime)
	scrollPct := m.viewport.ScrollPercent()
	statusBar := renderStatusBar(m.styles, m.vpConfig, m.state, phaseStr, elapsed,
		m.autoScroll, scrollPct, m.err)
	// pad to full width
	statusBarStyle := lipgloss.NewStyle().Width(m.width)
	b.WriteString(statusBarStyle.Render(statusBar))

	// hotkey hint bar
	b.WriteString("\n")
	b.WriteString(renderHotkeyBar(m.styles, m.state, m.width))

	return b.String()
}

// renderPlanSelect renders the plan selection view.
func (m Model) renderPlanSelect() string {
	if !m.planReady {
		return "loading plans..."
	}
	if m.emptyList {
		var b strings.Builder
		title := m.styles.DialogTitle.Render("Plan Selection")
		b.WriteString(title)
		b.WriteByte('\n')
		b.WriteString(m.styles.Warn.Render("No plans found in docs/plans/"))
		b.WriteString("\n\n")
		b.WriteString("press " + m.styles.HotkeyKey.Render("n") + " to create a new plan, or " +
			m.styles.HotkeyKey.Render("q") + " to quit")
		dialogWidth := max(m.width-6, 1)
		return m.styles.DialogBorder.Width(dialogWidth).Render(b.String())
	}
	return m.planList.View()
}

// renderPlanCreate renders the plan creation textarea view.
func (m Model) renderPlanCreate() string {
	if !m.planCreateReady {
		return "initializing editor..."
	}
	return m.planCreate.view(m.styles, m.width)
}

// renderQuestion renders the question view with selectable options inside a bordered dialog.
func (m Model) renderQuestion() string {
	var b strings.Builder

	title := m.styles.DialogTitle.Render("Question")
	b.WriteString(title)
	b.WriteByte('\n')

	b.WriteString(m.styles.Info.Render(m.question))
	b.WriteString("\n\n")

	for i, opt := range m.questionOpts {
		cursor := "  "
		if i == m.questionCursor {
			cursor = "> "
		}
		line := fmt.Sprintf("%s%d) %s", cursor, i+1, opt)
		if i == m.questionCursor {
			b.WriteString(m.styles.Task.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	dialogWidth := max(m.width-6, 1)
	return m.styles.DialogBorder.Width(dialogWidth).Render(b.String())
}
