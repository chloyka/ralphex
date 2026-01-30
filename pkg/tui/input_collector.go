package tui

import (
	"context"
	"errors"
	"fmt"
)

// teaCollector implements processor.InputCollector using Bubble Tea
// for interactive question/answer flow.
type teaCollector struct {
	sender   Sender
	answerCh chan string
}

// NewCollector creates a TUI input collector that sends questions to the
// Bubble Tea program and blocks until an answer is received.
func NewCollector(sender Sender) *teaCollector {
	return &teaCollector{
		sender:   sender,
		answerCh: make(chan string, 1),
	}
}

// AskQuestion sends a QuestionMsg to the TUI and blocks until the user selects
// an answer or the context is canceled.
func (c *teaCollector) AskQuestion(ctx context.Context, question string, options []string) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no options provided")
	}

	// drain any stale answer from a previous canceled question
	select {
	case <-c.answerCh:
	default:
	}

	c.sender.Send(QuestionMsg{
		Question: question,
		Options:  options,
		answerCh: c.answerCh,
	})

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("ask question: %w", ctx.Err())
	case answer, ok := <-c.answerCh:
		if !ok {
			return "", errors.New("question canceled")
		}
		return answer, nil
	}
}

// AskYesNo sends a yes/no question to the TUI and returns true if "Yes" is selected.
func (c *teaCollector) AskYesNo(ctx context.Context, question string) (bool, error) {
	answer, err := c.AskQuestion(ctx, question, []string{"Yes", "No"})
	if err != nil {
		return false, err
	}
	return answer == "Yes", nil
}
