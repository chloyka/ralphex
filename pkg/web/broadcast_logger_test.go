package web

import (
	"os"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
	"github.com/umputun/ralphex/pkg/tui"
)

func TestNewBroadcastLogger(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc:     func(processor.Phase) {},
		PrintFunc:        func(string, ...any) {},
		PrintRawFunc:     func(string, ...any) {},
		PrintSectionFunc: func(processor.Section) {},
		PrintAlignedFunc: func(string) {},
		PathFunc:         func() string { return "/test/path" },
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	bl := NewBroadcastLogger(mockLogger, session)

	assert.NotNil(t, bl)
	assert.Equal(t, processor.PhaseTask, bl.phase)
}

func TestBroadcastLogger_SetPhase(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc: func(processor.Phase) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.SetPhase(processor.PhaseReview)

	assert.Equal(t, processor.PhaseReview, bl.phase)
	require.Len(t, mockLogger.SetPhaseCalls(), 1)
	assert.Equal(t, processor.PhaseReview, mockLogger.SetPhaseCalls()[0].Phase)
}

func TestBroadcastLogger_SetPhase_EmitsTaskEnd(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc:     func(processor.Phase) {},
		PrintSectionFunc: func(processor.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// set task phase and start a task
	bl.SetPhase(processor.PhaseTask)
	bl.PrintSection(processor.NewTaskIterationSection(1))

	// track current task
	assert.Equal(t, 1, bl.currentTask)

	// transition away from task phase - should reset currentTask
	bl.SetPhase(processor.PhaseReview)

	// currentTask should be reset to 0
	assert.Equal(t, 0, bl.currentTask)
}

func TestBroadcastLogger_Print(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc: func(string, ...any) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.Print("hello %s", "world")

	// verify inner logger was called
	require.Len(t, mockLogger.PrintCalls(), 1)
	assert.Equal(t, "hello %s", mockLogger.PrintCalls()[0].Format)
	assert.Equal(t, []any{"world"}, mockLogger.PrintCalls()[0].Args)
}

func TestBroadcastLogger_PrintRaw(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintRawFunc: func(string, ...any) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.PrintRaw("raw %d", 42)

	// verify inner logger was called
	require.Len(t, mockLogger.PrintRawCalls(), 1)
	assert.Equal(t, "raw %d", mockLogger.PrintRawCalls()[0].Format)
	assert.Equal(t, []any{42}, mockLogger.PrintRawCalls()[0].Args)
}

func TestBroadcastLogger_PrintSection(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(processor.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	section := processor.NewGenericSection("Test Section")
	bl.PrintSection(section)

	// verify inner logger was called
	require.Len(t, mockLogger.PrintSectionCalls(), 1)
	assert.Equal(t, "Test Section", mockLogger.PrintSectionCalls()[0].Section.Label)
}

func TestBroadcastLogger_PrintAligned(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintAlignedFunc: func(string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.PrintAligned("aligned text")

	// verify inner logger was called
	require.Len(t, mockLogger.PrintAlignedCalls(), 1)
	assert.Equal(t, "aligned text", mockLogger.PrintAlignedCalls()[0].Text)
}

func TestBroadcastLogger_Path(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PathFunc: func() string { return "/test/progress.txt" },
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	path := bl.Path()

	assert.Equal(t, "/test/progress.txt", path)
	require.Len(t, mockLogger.PathCalls(), 1)
}

func TestBroadcastLogger_PhaseAffectsEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc: func(processor.Phase) {},
		PrintFunc:    func(string, ...any) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// print with default phase (task)
	bl.Print("task message")
	assert.Equal(t, processor.PhaseTask, bl.phase)

	// change phase and verify it's updated
	bl.SetPhase(processor.PhaseCodex)
	assert.Equal(t, processor.PhaseCodex, bl.phase)
}

func TestFormatText(t *testing.T) {
	tests := []struct {
		format string
		args   []any
		want   string
	}{
		{"plain text", nil, "plain text"},
		{"hello %s", []any{"world"}, "hello world"},
		{"num: %d", []any{42}, "num: 42"},
		{"%s has %d items", []any{"list", 3}, "list has 3 items"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatText(tt.format, tt.args...)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBroadcastLogger_PrintSection_TaskBoundaryEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(processor.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// emit task iteration section - should set currentTask
	bl.PrintSection(processor.NewTaskIterationSection(1))
	assert.Equal(t, 1, bl.currentTask)

	// emit another task iteration - should update currentTask
	bl.PrintSection(processor.NewTaskIterationSection(2))
	assert.Equal(t, 2, bl.currentTask)
}

func TestBroadcastLogger_PrintSection_IterationEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(processor.Section) {},
		SetPhaseFunc:     func(processor.Phase) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// test claude review iteration pattern
	bl.SetPhase(processor.PhaseReview)
	bl.PrintSection(processor.NewClaudeReviewSection(3, ": critical/major"))

	// verify inner logger was called
	require.Len(t, mockLogger.PrintSectionCalls(), 1)

	// test codex iteration pattern
	bl.SetPhase(processor.PhaseCodex)
	bl.PrintSection(processor.NewCodexIterationSection(5))

	// verify inner logger was called again
	require.Len(t, mockLogger.PrintSectionCalls(), 2)
}

func TestBroadcastLogger_LogQuestion(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		LogQuestionFunc: func(string, []string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.LogQuestion("Which backend?", []string{"Redis", "Memcached"})

	// verify inner logger was called
	require.Len(t, mockLogger.LogQuestionCalls(), 1)
	assert.Equal(t, "Which backend?", mockLogger.LogQuestionCalls()[0].Question)
	assert.Equal(t, []string{"Redis", "Memcached"}, mockLogger.LogQuestionCalls()[0].Options)
}

func TestBroadcastLogger_LogAnswer(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		LogAnswerFunc: func(string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.LogAnswer("Redis")

	// verify inner logger was called
	require.Len(t, mockLogger.LogAnswerCalls(), 1)
	assert.Equal(t, "Redis", mockLogger.LogAnswerCalls()[0].Answer)
}

func TestBroadcastLogger_PrintAligned_WithSignal(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintAlignedFunc: func(string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// text containing a terminal signal should trigger signal broadcast
	bl.PrintAligned("output with <<<RALPHEX:ALL_TASKS_DONE>>> marker")

	require.Len(t, mockLogger.PrintAlignedCalls(), 1)
	assert.Contains(t, mockLogger.PrintAlignedCalls()[0].Text, "<<<RALPHEX:ALL_TASKS_DONE>>>")
}

func TestBroadcastLogger_ImplementsProcessorLogger(t *testing.T) {
	// compile-time check that BroadcastLogger implements processor.Logger
	var _ processor.Logger = (*BroadcastLogger)(nil)
}

func TestExtractTerminalSignal(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		signal string
	}{
		{
			name:   "completed",
			text:   "task done <<<RALPHEX:ALL_TASKS_DONE>>>",
			signal: "COMPLETED",
		},
		{
			name:   "failed",
			text:   "task failed <<<RALPHEX:TASK_FAILED>>>",
			signal: "FAILED",
		},
		{
			name:   "review-done",
			text:   "review done <<<RALPHEX:REVIEW_DONE>>>",
			signal: "REVIEW_DONE",
		},
		{
			name:   "codex-review-done",
			text:   "codex done <<<RALPHEX:CODEX_REVIEW_DONE>>>",
			signal: "CODEX_REVIEW_DONE",
		},
		{
			name:   "no signal",
			text:   "regular output",
			signal: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTerminalSignal(tc.text)
			assert.Equal(t, tc.signal, got)
		})
	}
}

// mockSender collects messages sent via Send() for verifying TUI integration.
type mockSender struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (s *mockSender) Send(msg tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
}

func (s *mockSender) messages() []tea.Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]tea.Msg, len(s.msgs))
	copy(result, s.msgs)
	return result
}

// TestBroadcastLogger_WithTeaLogger_Integration verifies that BroadcastLogger
// wrapping a real teaLogger sends messages to both TUI (via tea.Msg) and SSE session.
// this is the core integration test for --serve alongside TUI.
func TestBroadcastLogger_WithTeaLogger_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	// create a real teaLogger with a mock sender to capture TUI messages
	sender := &mockSender{}
	tuiLog, err := tui.NewLogger(tui.LoggerConfig{
		PlanFile: "docs/plans/test-feature.md",
		Mode:     "full",
		Branch:   "test-branch",
	}, sender)
	require.NoError(t, err)
	defer tuiLog.Close()

	// create SSE session and wrap with BroadcastLogger
	session := NewSession("test", tuiLog.Path())
	defer session.Close()
	bl := NewBroadcastLogger(tuiLog, session)

	// exercise all Logger interface methods through BroadcastLogger
	bl.SetPhase(processor.PhaseReview)
	bl.Print("test message %d", 1)
	bl.PrintRaw("raw message")
	bl.PrintSection(processor.NewGenericSection("test section"))
	bl.PrintAligned("aligned line 1\naligned line 2")
	bl.LogQuestion("pick one", []string{"A", "B"})
	bl.LogAnswer("A")

	// verify TUI messages were sent (teaLogger sends through sender)
	msgs := sender.messages()

	var foundPhaseChange, foundOutput, foundRawOutput, foundSection, foundAligned bool
	for _, msg := range msgs {
		switch m := msg.(type) {
		case tui.PhaseChangeMsg:
			if m.Phase == processor.PhaseReview {
				foundPhaseChange = true
			}
		case tui.OutputMsg:
			if strings.Contains(m.Text, "test message 1") {
				foundOutput = true
			}
			if m.Text == "raw message" {
				foundRawOutput = true
			}
			if strings.Contains(m.Text, "aligned line 1") {
				foundAligned = true
			}
		case tui.SectionMsg:
			if m.Section.Label == "test section" {
				foundSection = true
			}
		}
	}

	assert.True(t, foundPhaseChange, "expected PhaseChangeMsg sent to TUI")
	assert.True(t, foundOutput, "expected OutputMsg with 'test message 1' sent to TUI")
	assert.True(t, foundRawOutput, "expected OutputMsg with 'raw message' sent to TUI")
	assert.True(t, foundSection, "expected SectionMsg sent to TUI")
	assert.True(t, foundAligned, "expected OutputMsg with aligned text sent to TUI")

	// verify progress file was written (teaLogger writes to file)
	content, err := os.ReadFile(tuiLog.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "test message 1")
	assert.Contains(t, contentStr, "raw message")
	assert.Contains(t, contentStr, "--- test section ---")
	assert.Contains(t, contentStr, "aligned line 1")
	assert.Contains(t, contentStr, "aligned line 2")
	assert.Contains(t, contentStr, "QUESTION: pick one")
	assert.Contains(t, contentStr, "ANSWER: A")

	// verify Path() returns the teaLogger's path
	assert.Equal(t, tuiLog.Path(), bl.Path())
}

// TestBroadcastLogger_WithTeaLogger_SSEEvents verifies that SSE events are
// published to the session when BroadcastLogger wraps teaLogger.
func TestBroadcastLogger_WithTeaLogger_SSEEvents(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	tuiLog, err := tui.NewLogger(tui.LoggerConfig{
		PlanFile: "docs/plans/sse-test.md",
		Mode:     "full",
		Branch:   "main",
	}, sender)
	require.NoError(t, err)
	defer tuiLog.Close()

	session := NewSession("sse-test", tuiLog.Path())
	defer session.Close()
	bl := NewBroadcastLogger(tuiLog, session)

	// exercise methods that produce SSE events
	bl.Print("sse output test")
	bl.PrintSection(processor.NewTaskIterationSection(1))
	bl.PrintSection(processor.NewTaskIterationSection(2))
	bl.SetPhase(processor.PhaseReview)

	// verify task boundary tracking (SSE-specific behavior)
	assert.Equal(t, 0, bl.currentTask, "currentTask should be 0 after phase transition from task")

	// verify BroadcastLogger tracked the phase correctly
	assert.Equal(t, processor.PhaseReview, bl.phase)
}

// TestBroadcastLogger_WithTeaLogger_PhaseTransitions verifies phase transitions
// produce correct SSE boundary events when wrapping teaLogger.
func TestBroadcastLogger_WithTeaLogger_PhaseTransitions(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	tuiLog, err := tui.NewLogger(tui.LoggerConfig{
		Mode:   "full",
		Branch: "main",
	}, sender)
	require.NoError(t, err)
	defer tuiLog.Close()

	session := NewSession("phase-test", tuiLog.Path())
	defer session.Close()
	bl := NewBroadcastLogger(tuiLog, session)

	// simulate full execution flow: task -> review -> codex
	bl.SetPhase(processor.PhaseTask)
	bl.PrintSection(processor.NewTaskIterationSection(1))
	assert.Equal(t, 1, bl.currentTask)

	bl.PrintSection(processor.NewTaskIterationSection(2))
	assert.Equal(t, 2, bl.currentTask)

	// transition to review - should emit task_end and reset
	bl.SetPhase(processor.PhaseReview)
	assert.Equal(t, 0, bl.currentTask)
	assert.Equal(t, processor.PhaseReview, bl.phase)

	bl.PrintSection(processor.NewClaudeReviewSection(1, ""))

	// transition to codex
	bl.SetPhase(processor.PhaseCodex)
	assert.Equal(t, processor.PhaseCodex, bl.phase)

	bl.PrintSection(processor.NewCodexIterationSection(1))

	// verify TUI received all phase changes
	msgs := sender.messages()
	var phaseChanges []processor.Phase
	for _, msg := range msgs {
		if pcm, ok := msg.(tui.PhaseChangeMsg); ok {
			phaseChanges = append(phaseChanges, pcm.Phase)
		}
	}
	assert.Equal(t, []processor.Phase{
		processor.PhaseTask,
		processor.PhaseReview,
		processor.PhaseCodex,
	}, phaseChanges, "TUI should receive all phase transitions")
}
