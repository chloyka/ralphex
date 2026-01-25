package processor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
)

// testAppConfig loads config with embedded defaults for testing.
func testAppConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	return cfg
}

// newMockExecutor creates a mock executor with predefined results.
func newMockExecutor(results []executor.Result) *mocks.ExecutorMock {
	idx := 0
	return &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			if idx >= len(results) {
				return executor.Result{Error: errors.New("no more mock results")}
			}
			result := results[idx]
			idx++
			return result
		},
	}
}

// newMockLogger creates a mock logger with no-op implementations.
func newMockLogger(path string) *mocks.LoggerMock {
	return &mocks.LoggerMock{
		SetPhaseFunc:     func(_ processor.Phase) {},
		PrintFunc:        func(_ string, _ ...any) {},
		PrintRawFunc:     func(_ string, _ ...any) {},
		PrintSectionFunc: func(_ processor.Section) {},
		PrintAlignedFunc: func(_ string) {},
		PathFunc:         func() string { return path },
	}
}

func TestRunner_Run_UnknownMode(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: "invalid"}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

func TestRunner_RunFull_NoPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: processor.ModeFull}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan file required")
}

func TestRunner_RunFull_Success(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	// all tasks complete - no [ ] checkboxes
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},    // task phase completes
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "done", Signal: processor.SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go"}, // codex finds issues
	})

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunFull_NoCodexFindings(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // codex finds nothing
	})

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_RunReviewOnly_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "done", Signal: processor.SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunCodexOnly_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "done", Signal: processor.SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunCodexOnly_NoFindings(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // no findings
	})

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_CodexDisabled_SkipsCodexPhase(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called when disabled")
}

func TestRunner_TaskPhase_FailedSignal(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: processor.SignalFailed}, // first try
		{Output: "error", Signal: processor.SignalFailed}, // retry
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_TaskPhase_MaxIterations(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "working..."},
		{Output: "still working..."},
		{Output: "more work..."},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 3, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max iterations")
}

func TestRunner_TaskPhase_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	log := newMockLogger("progress.txt")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunner_ClaudeReview_FailedSignal(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: processor.SignalFailed},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_CodexPhase_Error(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Error: errors.New("codex error")},
	})

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex")
	assert.Len(t, codex.RunCalls(), 1, "codex should be called once")
}

func TestRunner_ClaudeExecution_Error(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Error: errors.New("claude error")},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude execution")
}

func TestRunner_ConfigValues(t *testing.T) {
	tests := []struct {
		name               string
		iterationDelayMs   int
		taskRetryCount     int
		expectedDelay      time.Duration
		expectedRetryCount int
	}{
		{
			name:               "default values",
			iterationDelayMs:   0,
			taskRetryCount:     0,
			expectedDelay:      processor.DefaultIterationDelay,
			expectedRetryCount: 1,
		},
		{
			name:               "custom delay",
			iterationDelayMs:   500,
			taskRetryCount:     0,
			expectedDelay:      500 * time.Millisecond,
			expectedRetryCount: 1,
		},
		{
			name:               "custom retry count",
			iterationDelayMs:   0,
			taskRetryCount:     3,
			expectedDelay:      processor.DefaultIterationDelay,
			expectedRetryCount: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log := newMockLogger("")
			claude := newMockExecutor(nil)
			codex := newMockExecutor(nil)

			cfg := processor.Config{
				IterationDelayMs: tc.iterationDelayMs,
				TaskRetryCount:   tc.taskRetryCount,
			}
			r := processor.NewWithExecutors(cfg, log, claude, codex)

			testCfg := r.TestConfig()
			assert.Equal(t, tc.expectedDelay, testCfg.IterationDelay)
			assert.Equal(t, tc.expectedRetryCount, testCfg.TaskRetryCount)
		})
	}
}

func TestRunner_HasUncompletedTasks(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{
			name:     "all tasks completed",
			content:  "# Plan\n- [x] Task 1\n- [x] Task 2",
			expected: false,
		},
		{
			name:     "has uncompleted task",
			content:  "# Plan\n- [x] Task 1\n- [ ] Task 2",
			expected: true,
		},
		{
			name:     "no checkboxes",
			content:  "# Plan\nJust some text",
			expected: false,
		},
		{
			name:     "uncompleted in nested list",
			content:  "# Plan\n- [x] Task 1\n  - [ ] Subtask",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			planFile := filepath.Join(tmpDir, "plan.md")
			require.NoError(t, os.WriteFile(planFile, []byte(tc.content), 0o600))

			log := newMockLogger("")
			claude := newMockExecutor(nil)
			codex := newMockExecutor(nil)

			cfg := processor.Config{PlanFile: planFile}
			r := processor.NewWithExecutors(cfg, log, claude, codex)

			assert.Equal(t, tc.expected, r.TestHasUncompletedTasks())
		})
	}
}

func TestRunner_TaskRetryCount_UsedCorrectly(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")

	// test with TaskRetryCount=2 - should retry twice before failing
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: processor.SignalFailed}, // first try
		{Output: "error", Signal: processor.SignalFailed}, // retry 1
		{Output: "error", Signal: processor.SignalFailed}, // retry 2
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:           processor.ModeFull,
		PlanFile:       planFile,
		MaxIterations:  10,
		TaskRetryCount: 2,
		// use 1ms delay for faster tests
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
	// should have tried 3 times: initial + 2 retries
	assert.Len(t, claude.RunCalls(), 3)
}
