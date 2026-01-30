package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/processor"
)

// mockSender collects messages sent via Send().
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

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name     string
		cfg      LoggerConfig
		wantPath string
	}{
		{
			name:     "full mode with plan",
			cfg:      LoggerConfig{PlanFile: "docs/plans/feature.md", Mode: "full", Branch: "main"},
			wantPath: "progress-feature.txt",
		},
		{
			name:     "review mode with plan",
			cfg:      LoggerConfig{PlanFile: "docs/plans/feature.md", Mode: "review", Branch: "main"},
			wantPath: "progress-feature-review.txt",
		},
		{
			name:     "codex-only mode with plan",
			cfg:      LoggerConfig{PlanFile: "docs/plans/feature.md", Mode: "codex-only", Branch: "main"},
			wantPath: "progress-feature-codex.txt",
		},
		{
			name:     "full mode no plan",
			cfg:      LoggerConfig{Mode: "full", Branch: "main"},
			wantPath: "progress.txt",
		},
		{
			name:     "plan mode with description",
			cfg:      LoggerConfig{Mode: "plan", PlanDescription: "implement caching", Branch: "main"},
			wantPath: "progress-plan-implement-caching.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			origDir, _ := os.Getwd()
			require.NoError(t, os.Chdir(tmpDir))
			defer func() { _ = os.Chdir(origDir) }()

			sender := &mockSender{}
			l, err := NewLogger(tc.cfg, sender)
			require.NoError(t, err)
			defer l.Close()

			assert.Equal(t, tc.wantPath, filepath.Base(l.Path()))

			// verify header written to file
			content, err := os.ReadFile(l.Path())
			require.NoError(t, err)
			assert.Contains(t, string(content), "# Ralphex Progress Log")
			assert.Contains(t, string(content), "Mode: "+tc.cfg.Mode)
		})
	}
}

func TestTeaLogger_Path(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)
	defer l.Close()

	assert.NotEmpty(t, l.Path())
	assert.True(t, strings.HasSuffix(l.Path(), "progress.txt"))
}

func TestTeaLogger_Path_NilFile(t *testing.T) {
	l := &teaLogger{}
	assert.Empty(t, l.Path())
}

func TestTeaLogger_SetPhase(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)
	defer l.Close()

	l.SetPhase(processor.PhaseReview)
	assert.Equal(t, processor.PhaseReview, l.phase)

	msgs := sender.messages()
	require.NotEmpty(t, msgs)

	// find PhaseChangeMsg
	var found bool
	for _, msg := range msgs {
		if pcm, ok := msg.(PhaseChangeMsg); ok {
			assert.Equal(t, processor.PhaseReview, pcm.Phase)
			found = true
			break
		}
	}
	assert.True(t, found, "expected PhaseChangeMsg to be sent")
}

func TestTeaLogger_Print(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)
	defer l.Close()

	l.Print("test message %d", 42)

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "test message 42")

	// check OutputMsg sent
	msgs := sender.messages()
	var outputFound bool
	for _, msg := range msgs {
		if om, ok := msg.(OutputMsg); ok && strings.Contains(om.Text, "test message 42") {
			outputFound = true
			// should contain timestamp
			assert.Contains(t, om.Text, "[")
			assert.Contains(t, om.Text, "]")
			break
		}
	}
	assert.True(t, outputFound, "expected OutputMsg with 'test message 42'")
}

func TestTeaLogger_PrintRaw(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)
	defer l.Close()

	l.PrintRaw("raw output %s", "data")

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "raw output data")

	// check OutputMsg sent without timestamp
	msgs := sender.messages()
	var outputFound bool
	for _, msg := range msgs {
		if om, ok := msg.(OutputMsg); ok && om.Text == "raw output data" {
			outputFound = true
			break
		}
	}
	assert.True(t, outputFound, "expected OutputMsg with 'raw output data'")
}

func TestTeaLogger_PrintSection(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)
	defer l.Close()

	section := processor.NewGenericSection("test section")
	l.PrintSection(section)

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "--- test section ---")

	// check SectionMsg sent
	msgs := sender.messages()
	var sectionFound bool
	for _, msg := range msgs {
		if sm, ok := msg.(SectionMsg); ok {
			assert.Equal(t, "test section", sm.Section.Label)
			sectionFound = true
			break
		}
	}
	assert.True(t, sectionFound, "expected SectionMsg to be sent")
}

func TestTeaLogger_PrintAligned(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)
	defer l.Close()

	l.PrintAligned("first line\nsecond line\nthird line")

	// check file output has timestamps per line
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "] first line")
	assert.Contains(t, contentStr, "] second line")
	assert.Contains(t, contentStr, "] third line")

	// check OutputMsg sent with all lines
	msgs := sender.messages()
	var outputFound bool
	for _, msg := range msgs {
		if om, ok := msg.(OutputMsg); ok && strings.Contains(om.Text, "first line") {
			assert.Contains(t, om.Text, "second line")
			assert.Contains(t, om.Text, "third line")
			outputFound = true
			break
		}
	}
	assert.True(t, outputFound, "expected OutputMsg with aligned text")
}

func TestTeaLogger_PrintAligned_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)
	defer l.Close()

	msgCountBefore := len(sender.messages())
	l.PrintAligned("")
	l.PrintAligned("\n\n")

	// no new messages should be sent
	assert.Len(t, sender.messages(), msgCountBefore)
}

func TestTeaLogger_PrintAligned_SkipsEmptyLines(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)
	defer l.Close()

	l.PrintAligned("line1\n\nline2")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "] line1")
	assert.Contains(t, contentStr, "] line2")
}

func TestTeaLogger_LogQuestion(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "plan", PlanDescription: "test", Branch: "main"}, sender)
	require.NoError(t, err)
	defer l.Close()

	l.LogQuestion("Which cache backend?", []string{"Redis", "In-memory", "File-based"})

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "QUESTION: Which cache backend?")
	assert.Contains(t, contentStr, "OPTIONS: Redis, In-memory, File-based")

	// check OutputMsgs sent
	msgs := sender.messages()
	var questionFound, optionsFound bool
	for _, msg := range msgs {
		if om, ok := msg.(OutputMsg); ok {
			if strings.Contains(om.Text, "QUESTION: Which cache backend?") {
				questionFound = true
			}
			if strings.Contains(om.Text, "OPTIONS: Redis, In-memory, File-based") {
				optionsFound = true
			}
		}
	}
	assert.True(t, questionFound, "expected OutputMsg with question")
	assert.True(t, optionsFound, "expected OutputMsg with options")
}

func TestTeaLogger_LogAnswer(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "plan", PlanDescription: "test", Branch: "main"}, sender)
	require.NoError(t, err)
	defer l.Close()

	l.LogAnswer("Redis")

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "ANSWER: Redis")

	// check OutputMsg sent
	msgs := sender.messages()
	var answerFound bool
	for _, msg := range msgs {
		if om, ok := msg.(OutputMsg); ok && strings.Contains(om.Text, "ANSWER: Redis") {
			answerFound = true
			break
		}
	}
	assert.True(t, answerFound, "expected OutputMsg with answer")
}

func TestTeaLogger_Close(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
	require.NoError(t, err)

	l.Print("some output")
	err = l.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "Completed:")
	assert.Contains(t, contentStr, strings.Repeat("-", 60))
}

func TestTeaLogger_Close_NilFile(t *testing.T) {
	l := &teaLogger{}
	assert.NoError(t, l.Close())
}

func TestTeaLogger_FileFormat(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{
		PlanFile: "docs/plans/feature.md",
		Mode:     "full",
		Branch:   "feature-branch",
	}, sender)
	require.NoError(t, err)
	defer l.Close()

	// verify file header format matches existing progress logger
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "# Ralphex Progress Log")
	assert.Contains(t, contentStr, "Plan: docs/plans/feature.md")
	assert.Contains(t, contentStr, "Branch: feature-branch")
	assert.Contains(t, contentStr, "Mode: full")
	assert.Contains(t, contentStr, "Started: ")
	assert.Contains(t, contentStr, strings.Repeat("-", 60))
}

func TestTeaLogger_FileFormat_NoPlan(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	sender := &mockSender{}
	l, err := NewLogger(LoggerConfig{Mode: "review", Branch: "main"}, sender)
	require.NoError(t, err)
	defer l.Close()

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "Plan: (no plan - review only)")
}

func TestTeaLogger_PrintSection_Varieties(t *testing.T) {
	tests := []struct {
		name        string
		section     processor.Section
		wantLabel   string
		wantFileStr string
	}{
		{
			name:        "task iteration",
			section:     processor.NewTaskIterationSection(1),
			wantLabel:   "task iteration 1",
			wantFileStr: "--- task iteration 1 ---",
		},
		{
			name:        "claude review",
			section:     processor.NewClaudeReviewSection(2, ""),
			wantLabel:   "claude review 2",
			wantFileStr: "--- claude review 2 ---",
		},
		{
			name:        "codex iteration",
			section:     processor.NewCodexIterationSection(3),
			wantLabel:   "codex iteration 3",
			wantFileStr: "--- codex iteration 3 ---",
		},
		{
			name:        "generic section",
			section:     processor.NewGenericSection("custom"),
			wantLabel:   "custom",
			wantFileStr: "--- custom ---",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			origDir, _ := os.Getwd()
			require.NoError(t, os.Chdir(tmpDir))
			defer func() { _ = os.Chdir(origDir) }()

			sender := &mockSender{}
			l, err := NewLogger(LoggerConfig{Mode: "full", Branch: "test"}, sender)
			require.NoError(t, err)
			defer l.Close()

			l.PrintSection(tc.section)

			content, err := os.ReadFile(l.Path())
			require.NoError(t, err)
			assert.Contains(t, string(content), tc.wantFileStr)

			msgs := sender.messages()
			var found bool
			for _, msg := range msgs {
				if sm, ok := msg.(SectionMsg); ok {
					assert.Equal(t, tc.wantLabel, sm.Section.Label)
					found = true
					break
				}
			}
			assert.True(t, found, "expected SectionMsg")
		})
	}
}

func TestTeaLogger_ImplementsProcessorLogger(t *testing.T) {
	// compile-time check that teaLogger implements processor.Logger
	var _ processor.Logger = (*teaLogger)(nil)
}

func TestProgressFilename(t *testing.T) {
	tests := []struct {
		name            string
		planFile        string
		planDescription string
		mode            string
		want            string
	}{
		{"full mode with plan", "docs/plans/feature.md", "", "full", "progress-feature.txt"},
		{"review mode with plan", "docs/plans/feature.md", "", "review", "progress-feature-review.txt"},
		{"codex-only mode with plan", "docs/plans/feature.md", "", "codex-only", "progress-feature-codex.txt"},
		{"full mode no plan", "", "", "full", "progress.txt"},
		{"review mode no plan", "", "", "review", "progress-review.txt"},
		{"codex-only mode no plan", "", "", "codex-only", "progress-codex.txt"},
		{"plan mode with description", "", "implement caching", "plan", "progress-plan-implement-caching.txt"},
		{"plan mode no description", "", "", "plan", "progress-plan.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := progressFilename(tc.planFile, tc.planDescription, tc.mode)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSanitizePlanName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple words", "implement caching", "implement-caching"},
		{"uppercase", "Add User Auth", "add-user-auth"},
		{"special chars", "fix: bug #123", "fix-bug-123"},
		{"empty string", "", "unnamed"},
		{"only special chars", "!@#$%", "unnamed"},
		{"long description", strings.Repeat("a", 60), strings.Repeat("a", 50)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizePlanName(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFormatListItem(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"numbered list", "1. first item", "  1. first item"},
		{"bullet list", "- bullet item", "  - bullet item"},
		{"regular text", "regular text", "regular text"},
		{"already indented", "  - item", "  - item"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatListItem(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsListItem(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1. first item", true},
		{"- bullet item", true},
		{"* star item", true},
		{"regular text", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := isListItem(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMockSender(t *testing.T) {
	s := &mockSender{}
	assert.Empty(t, s.messages())

	s.Send(OutputMsg{Text: "hello"})
	s.Send(PhaseChangeMsg{Phase: processor.PhaseTask})

	msgs := s.messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, OutputMsg{Text: "hello"}, msgs[0])
	assert.Equal(t, PhaseChangeMsg{Phase: processor.PhaseTask}, msgs[1])
}

func TestSafeSender(t *testing.T) {
	t.Run("forwards_messages_before_stop", func(t *testing.T) {
		inner := &mockSender{}
		ss := NewSafeSender(inner)

		ss.Send(OutputMsg{Text: "before stop"})
		ss.Send(PhaseChangeMsg{Phase: processor.PhaseTask})

		msgs := inner.messages()
		require.Len(t, msgs, 2)
		assert.Equal(t, OutputMsg{Text: "before stop"}, msgs[0])
		assert.Equal(t, PhaseChangeMsg{Phase: processor.PhaseTask}, msgs[1])
	})

	t.Run("discards_messages_after_stop", func(t *testing.T) {
		inner := &mockSender{}
		ss := NewSafeSender(inner)

		ss.Send(OutputMsg{Text: "before"})
		ss.Stop()
		ss.Send(OutputMsg{Text: "after"})
		ss.Send(ExecutionDoneMsg{Err: nil})

		msgs := inner.messages()
		require.Len(t, msgs, 1)
		assert.Equal(t, OutputMsg{Text: "before"}, msgs[0])
	})

	t.Run("stop_is_idempotent", func(t *testing.T) {
		inner := &mockSender{}
		ss := NewSafeSender(inner)

		ss.Stop()
		ss.Stop() // should not panic
		ss.Stop()

		// send after stop should be no-op
		ss.Send(OutputMsg{Text: "ignored"})
		assert.Empty(t, inner.messages())
	})

	t.Run("concurrent_send_and_stop", func(t *testing.T) {
		inner := &mockSender{}
		ss := NewSafeSender(inner)

		var wg sync.WaitGroup
		// start multiple goroutines sending messages
		for range 100 {
			wg.Go(func() {
				ss.Send(OutputMsg{Text: "msg"})
			})
		}

		// stop concurrently
		wg.Go(func() {
			ss.Stop()
		})

		wg.Wait()
		// should not panic or deadlock; some messages may have been delivered
		assert.LessOrEqual(t, len(inner.messages()), 100)
	})

	t.Run("implements_sender_interface", func(t *testing.T) {
		inner := &mockSender{}
		ss := NewSafeSender(inner)
		var _ Sender = ss // compile-time check
	})
}
