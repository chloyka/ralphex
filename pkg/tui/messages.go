package tui

import (
	"errors"

	"github.com/umputun/ralphex/pkg/processor"
)

// ErrNoPlansFound is returned when no plan files exist in the plans directory.
var ErrNoPlansFound = errors.New("no plans found")

// ErrUserCanceled is returned when the user cancels plan selection.
var ErrUserCanceled = errors.New("user canceled")

// OutputMsg carries a line of text output to display in the viewport.
type OutputMsg struct {
	Text string
}

// SectionMsg carries a section header event.
type SectionMsg struct {
	Section processor.Section
}

// PhaseChangeMsg indicates a phase transition.
type PhaseChangeMsg struct {
	Phase processor.Phase
}

// QuestionMsg presents a question with selectable options.
// answerCh is used by the collector to receive the user's selection.
type QuestionMsg struct {
	Question string
	Options  []string
	answerCh chan string // channel for sending answer back to collector
}

// ExecutionDoneMsg signals that execution has finished.
type ExecutionDoneMsg struct {
	Err error
}

// ErrorMsg carries an error to display.
type ErrorMsg struct {
	Err error
}

// PlanSelectedMsg carries the selected plan file path.
type PlanSelectedMsg struct {
	Path string
}

// PlanCreatedMsg carries the description text from plan creation.
type PlanCreatedMsg struct {
	Description string
}

// StartupInfoMsg carries startup information to display in the TUI header.
type StartupInfoMsg struct {
	PlanFile string
	Branch   string
}

// PlanSelectionResult carries the result of plan selection from the TUI.
type PlanSelectionResult struct {
	Path        string // selected plan file path
	Description string // plan description (if creating new plan)
	Err         error
}

// PlanSelectionRequestMsg requests the TUI to register a result channel for plan selection.
type PlanSelectionRequestMsg struct {
	ResultCh chan PlanSelectionResult
}
