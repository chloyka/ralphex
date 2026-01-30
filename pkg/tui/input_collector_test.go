package tui

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewCollector(t *testing.T) {
	sender := &mockSender{}
	c := NewCollector(sender)
	assert.NotNil(t, c)
	assert.NotNil(t, c.answerCh)
	assert.NotNil(t, c.sender)
}

func TestTeaCollector_AskQuestion_RoundTrip(t *testing.T) {
	sender := &mockSender{}
	c := NewCollector(sender)

	// run AskQuestion in a goroutine since it blocks
	var answer string
	var askErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		answer, askErr = c.AskQuestion(context.Background(), "pick one", []string{"A", "B", "C"})
	}()

	// wait for the message to be sent
	require.Eventually(t, func() bool {
		return len(sender.messages()) > 0
	}, time.Second, 10*time.Millisecond)

	// verify QuestionMsg was sent
	msgs := sender.messages()
	require.Len(t, msgs, 1)
	qMsg, ok := msgs[0].(QuestionMsg)
	require.True(t, ok)
	assert.Equal(t, "pick one", qMsg.Question)
	assert.Equal(t, []string{"A", "B", "C"}, qMsg.Options)

	// send answer through the channel
	qMsg.answerCh <- "B"

	// wait for AskQuestion to return
	<-done
	require.NoError(t, askErr)
	assert.Equal(t, "B", answer)
}

func TestTeaCollector_AskQuestion_ContextCanceled(t *testing.T) {
	sender := &mockSender{}
	c := NewCollector(sender)

	ctx, cancel := context.WithCancel(context.Background())

	var askErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, askErr = c.AskQuestion(ctx, "pick one", []string{"A", "B"})
	}()

	// wait for the message to be sent, then cancel
	require.Eventually(t, func() bool {
		return len(sender.messages()) > 0
	}, time.Second, 10*time.Millisecond)

	cancel()

	<-done
	require.Error(t, askErr)
	assert.ErrorIs(t, askErr, context.Canceled)
}

func TestTeaCollector_AskQuestion_EmptyOptions(t *testing.T) {
	sender := &mockSender{}
	c := NewCollector(sender)

	_, err := c.AskQuestion(context.Background(), "pick one", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no options provided")

	_, err = c.AskQuestion(context.Background(), "pick one", []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no options provided")
}

func TestTeaCollector_AskYesNo(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{name: "yes", answer: "Yes", want: true},
		{name: "no", answer: "No", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender := &mockSender{}
			c := NewCollector(sender)

			var result bool
			var askErr error
			done := make(chan struct{})
			go func() {
				defer close(done)
				result, askErr = c.AskYesNo(context.Background(), "continue?")
			}()

			// wait for the message to be sent
			require.Eventually(t, func() bool {
				return len(sender.messages()) > 0
			}, time.Second, 10*time.Millisecond)

			// verify Yes/No options
			msgs := sender.messages()
			qMsg, ok := msgs[0].(QuestionMsg)
			require.True(t, ok)
			assert.Equal(t, []string{"Yes", "No"}, qMsg.Options)

			// send answer
			qMsg.answerCh <- tc.answer

			<-done
			require.NoError(t, askErr)
			assert.Equal(t, tc.want, result)
		})
	}
}

func TestTeaCollector_AskYesNo_ContextCanceled(t *testing.T) {
	sender := &mockSender{}
	c := NewCollector(sender)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := c.AskYesNo(ctx, "continue?")
	require.Error(t, err)
	assert.False(t, result)
}

func TestTeaCollector_AskQuestion_ChannelClosed(t *testing.T) {
	sender := &mockSender{}
	c := NewCollector(sender)

	var askErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, askErr = c.AskQuestion(context.Background(), "pick one", []string{"A", "B"})
	}()

	// wait for the message to be sent
	require.Eventually(t, func() bool {
		return len(sender.messages()) > 0
	}, time.Second, 10*time.Millisecond)

	// close the channel (simulates Ctrl+C closing questionCh)
	close(c.answerCh)

	<-done
	require.Error(t, askErr)
	assert.Contains(t, askErr.Error(), "question canceled")
}

func TestTeaCollector_ImplementsCollectorInterface(t *testing.T) {
	// compile-time check: teaCollector implements processor.InputCollector
	sender := &mockSender{}
	c := NewCollector(sender)
	var _ interface {
		AskQuestion(ctx context.Context, question string, options []string) (string, error)
	} = c
}

func TestModel_QuestionFlow(t *testing.T) {
	m := NewModel(StateExecuting)

	// initialize viewport
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// send a question
	answerCh := make(chan string, 1)
	updated, _ = m.Update(QuestionMsg{
		Question: "Which database?",
		Options:  []string{"PostgreSQL", "MySQL", "SQLite"},
		answerCh: answerCh,
	})
	m = updated.(Model)

	assert.Equal(t, StateQuestion, m.state)
	assert.Equal(t, "Which database?", m.question)
	assert.Equal(t, []string{"PostgreSQL", "MySQL", "SQLite"}, m.questionOpts)
	assert.Equal(t, 0, m.questionCursor)
	assert.Equal(t, StateExecuting, m.prevState)

	// navigate down
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 1, m.questionCursor)

	// navigate down again
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 2, m.questionCursor)

	// navigate up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.Equal(t, 1, m.questionCursor)

	// select with enter
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	// should return to previous state
	assert.Equal(t, StateExecuting, m.state)
	assert.Empty(t, m.question)
	assert.Nil(t, m.questionOpts)

	// verify the answer was sent to channel
	select {
	case answer := <-answerCh:
		assert.Equal(t, "MySQL", answer)
	default:
		t.Fatal("expected answer to be sent to channel")
	}
}

func TestModel_QuestionFlow_VimKeys(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	answerCh := make(chan string, 1)
	updated, _ = m.Update(QuestionMsg{
		Question: "pick",
		Options:  []string{"A", "B"},
		answerCh: answerCh,
	})
	m = updated.(Model)

	// navigate with j/k
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	assert.Equal(t, 1, m.questionCursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = updated.(Model)
	assert.Equal(t, 0, m.questionCursor)
}

func TestModel_QuestionFlow_BoundsCheck(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	answerCh := make(chan string, 1)
	updated, _ = m.Update(QuestionMsg{
		Question: "pick",
		Options:  []string{"A", "B"},
		answerCh: answerCh,
	})
	m = updated.(Model)

	// try to go above 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	assert.Equal(t, 0, m.questionCursor)

	// go to end and try to go past
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 1, m.questionCursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	assert.Equal(t, 1, m.questionCursor)
}

func TestModel_QuestionView(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	answerCh := make(chan string, 1)
	updated, _ = m.Update(QuestionMsg{
		Question: "Which option?",
		Options:  []string{"First", "Second", "Third"},
		answerCh: answerCh,
	})
	m = updated.(Model)

	view := m.View()
	assert.Contains(t, view, "Which option?")
	assert.Contains(t, view, "First")
	assert.Contains(t, view, "Second")
	assert.Contains(t, view, "Third")
	assert.Contains(t, view, "navigate")
	assert.Contains(t, view, "select")

	// cursor indicator should be visible
	assert.Contains(t, view, ">")
}

func TestModel_QuestionCtrlC(t *testing.T) {
	m := NewModel(StateExecuting)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	answerCh := make(chan string, 1)
	updated, _ = m.Update(QuestionMsg{
		Question: "pick",
		Options:  []string{"A"},
		answerCh: answerCh,
	})
	m = updated.(Model)

	// ctrl+c during question should quit
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.NotNil(t, cmd, "ctrl+c during question should produce quit command")
}
