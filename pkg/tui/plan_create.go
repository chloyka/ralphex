package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// planCreateModel holds the textarea for plan description input.
type planCreateModel struct {
	textarea textarea.Model
}

// newPlanCreate creates a configured textarea model for plan description input.
func newPlanCreate(width, height int) planCreateModel {
	ta := textarea.New()
	ta.Placeholder = "describe the plan you want to create..."
	ta.Focus()
	ta.CharLimit = 0 // no limit
	if width > 10 {
		ta.SetWidth(width - 10) // leave margin for border
	}
	if height > 12 {
		ta.SetHeight(height - 12) // leave room for border, title, and description
	}
	return planCreateModel{textarea: ta}
}

// initPlanCreateMsg is sent to initialize the plan creation textarea.
type initPlanCreateMsg struct{}

// planCreateSubmitMsg is sent when the user submits the plan description.
type planCreateSubmitMsg struct {
	description string
}

// planCreateCancelMsg is sent when the user cancels plan creation.
type planCreateCancelMsg struct{}

// updatePlanCreate handles key events and messages for the plan creation textarea.
// returns the updated model and a tea.Cmd.
func (pc planCreateModel) updatePlanCreate(msg tea.Msg) (planCreateModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type { //nolint:exhaustive // only special keys handled; rest delegated to textarea
		case tea.KeyCtrlD, tea.KeyEsc:
			text := pc.textarea.Value()
			if text != "" {
				return pc, func() tea.Msg {
					return planCreateSubmitMsg{description: text}
				}
			}
			// empty text on Esc means cancel
			if keyMsg.Type == tea.KeyEsc {
				return pc, func() tea.Msg { return planCreateCancelMsg{} }
			}
			return pc, nil
		case tea.KeyCtrlC:
			return pc, func() tea.Msg { return planCreateCancelMsg{} }
		}
	}

	// delegate to textarea for all other messages
	var cmd tea.Cmd
	pc.textarea, cmd = pc.textarea.Update(msg)
	return pc, cmd
}

// view renders the plan creation textarea with header and instructions inside a bordered dialog.
func (pc planCreateModel) view(styles *Styles, width int) string {
	var b strings.Builder
	b.Grow(512)

	title := styles.DialogTitle.Render("Create New Plan")
	b.WriteString(title)
	b.WriteByte('\n')

	desc := styles.Info.Render("Describe what you want to build. Be specific about features,\nrequirements, and goals. Claude will ask clarifying questions.")
	b.WriteString(desc)
	b.WriteString("\n\n")

	b.WriteString(pc.textarea.View())

	dialogWidth := max(width-6, 1)
	return styles.DialogBorder.Width(dialogWidth).Render(b.String())
}
