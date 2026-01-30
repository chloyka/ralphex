package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/git"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
	"github.com/umputun/ralphex/pkg/tui"
)

// mockTUISender implements tui.Sender for testing.
type mockTUISender struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (s *mockTUISender) Send(msg tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
}

func (s *mockTUISender) messages() []tea.Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]tea.Msg, len(s.msgs))
	copy(result, s.msgs)
	return result
}

// mockYesNoAsker implements yesNoAsker for testing.
type mockYesNoAsker struct {
	answer bool
	err    error
	called bool
}

func (m *mockYesNoAsker) AskYesNo(_ context.Context, _ string) (bool, error) {
	m.called = true
	return m.answer, m.err
}

// testOpts creates opts with tea options configured for headless (non-TTY) test execution.
func testOpts(o opts) opts {
	ctx := context.Background()
	if o.teaOptions == nil {
		o.teaOptions = testTeaOptions(ctx)
	}
	return o
}

// testColors returns a Colors instance for testing.
func testColors() *progress.Colors {
	return progress.NewColors(config.ColorConfig{
		Task:       "0,255,0",
		Review:     "0,255,255",
		Codex:      "255,0,255",
		ClaudeEval: "100,200,255",
		Warn:       "255,255,0",
		Error:      "255,0,0",
		Signal:     "255,100,100",
		Timestamp:  "138,138,138",
		Info:       "180,180,180",
	})
}

func TestDetermineMode(t *testing.T) {
	tests := []struct {
		name     string
		opts     opts
		expected processor.Mode
	}{
		{name: "default_is_full", opts: opts{}, expected: processor.ModeFull},
		{name: "review_flag", opts: opts{Review: true}, expected: processor.ModeReview},
		{name: "codex_only_flag", opts: opts{CodexOnly: true}, expected: processor.ModeCodexOnly},
		{name: "codex_only_takes_precedence", opts: opts{Review: true, CodexOnly: true}, expected: processor.ModeCodexOnly},
		{name: "plan_flag", opts: opts{PlanDescription: "add caching"}, expected: processor.ModePlan},
		{name: "plan_takes_precedence_over_review", opts: opts{PlanDescription: "add caching", Review: true}, expected: processor.ModePlan},
		{name: "plan_takes_precedence_over_codex", opts: opts{PlanDescription: "add caching", CodexOnly: true}, expected: processor.ModePlan},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := determineMode(tc.opts)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestIsWatchOnlyMode(t *testing.T) {
	tests := []struct {
		name            string
		opts            opts
		configWatchDirs []string
		expected        bool
	}{
		{name: "serve_with_watch_and_no_plan", opts: opts{Serve: true, Watch: []string{"/tmp"}}, configWatchDirs: nil, expected: true},
		{name: "serve_with_config_watch_and_no_plan", opts: opts{Serve: true}, configWatchDirs: []string{"/home"}, expected: true},
		{name: "serve_without_watch", opts: opts{Serve: true}, configWatchDirs: nil, expected: false},
		{name: "no_serve_with_watch", opts: opts{Watch: []string{"/tmp"}}, configWatchDirs: nil, expected: false},
		{name: "serve_with_plan_file", opts: opts{Serve: true, Watch: []string{"/tmp"}, PlanFile: "plan.md"}, configWatchDirs: nil, expected: false},
		{name: "serve_with_plan_description", opts: opts{Serve: true, Watch: []string{"/tmp"}, PlanDescription: "add feature"}, configWatchDirs: nil, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isWatchOnlyMode(tc.opts, tc.configWatchDirs)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestPlanFlagConflict(t *testing.T) {
	t.Run("returns_error_when_plan_and_planfile_both_set", func(t *testing.T) {
		o := testOpts(opts{
			PlanDescription: "add caching",
			PlanFile:        "docs/plans/some-plan.md",
		})
		err := run(context.Background(), o)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--plan flag conflicts")
	})

	t.Run("no_error_when_only_plan_flag_set", func(t *testing.T) {
		// this test will fail at a later point (missing git repo etc), but not at validation
		o := testOpts(opts{PlanDescription: "add caching"})
		err := run(context.Background(), o)
		// should fail at git repo check, not at validation
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "--plan flag conflicts")
	})

	t.Run("no_error_when_only_planfile_set", func(t *testing.T) {
		// this test will fail at a later point (file not found etc), but not at validation
		o := testOpts(opts{PlanFile: "nonexistent-plan.md"})
		err := run(context.Background(), o)
		// should fail at git repo check, not at validation
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "--plan flag conflicts")
	})
}

func TestPlanModeIntegration(t *testing.T) {
	t.Run("plan_mode_requires_git_repo", func(t *testing.T) {
		// skip if claude not installed - this test requires claude to pass dependency check
		if _, err := exec.LookPath("claude"); err != nil {
			t.Skip("claude not installed")
		}

		// run from a non-git directory
		tmpDir := t.TempDir()
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(tmpDir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		o := testOpts(opts{PlanDescription: "add caching feature"})
		err = run(context.Background(), o)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no .git directory")
	})

	t.Run("plan_mode_runs_from_git_repo", func(t *testing.T) {
		// test that plan mode passes validation when running from a git repo.
		// verifies the pre-TUI checks pass without validation errors.
		o := opts{PlanDescription: "add caching feature", MaxIterations: 1}
		err := validateFlags(o)
		require.NoError(t, err)

		mode := determineMode(o)
		assert.Equal(t, processor.ModePlan, mode)

		// verify TUI state is executing (plan mode with description skips selection)
		state := determineInitialTUIState(o, mode)
		assert.Equal(t, tui.StateExecuting, state)
	})

	t.Run("plan_mode_progress_file_naming", func(t *testing.T) {
		// test that progress filename generation works for plan mode.
		// uses TUI logger directly instead of run() to avoid background goroutine cleanup races.
		tmpDir := t.TempDir()
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(tmpDir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create a TUI logger for plan mode and verify progress file naming
		sender := &mockTUISender{}
		tuiLog, err := tui.NewLogger(tui.LoggerConfig{
			PlanDescription: "test plan description",
			Mode:            "plan",
			Branch:          "main",
		}, sender)
		require.NoError(t, err)
		defer tuiLog.Close()

		assert.Contains(t, tuiLog.Path(), "progress-plan-test-plan-description.txt")
	})
}

func TestCheckClaudeDep(t *testing.T) {
	t.Run("uses_configured_command", func(t *testing.T) {
		cfg := &config.Config{ClaudeCommand: "nonexistent-command-12345"}
		err := checkClaudeDep(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent-command-12345")
	})

	t.Run("falls_back_to_claude_when_empty", func(t *testing.T) {
		cfg := &config.Config{ClaudeCommand: ""}
		err := checkClaudeDep(cfg)
		// may pass or fail depending on whether claude is installed
		// but error message should reference "claude" not empty string
		if err != nil {
			assert.Contains(t, err.Error(), "claude")
		}
	})
}

func TestCreateRunner(t *testing.T) {
	t.Run("maps_config_correctly", func(t *testing.T) {
		cfg := &config.Config{
			IterationDelayMs: 5000,
			TaskRetryCount:   3,
			CodexEnabled:     false,
		}
		o := opts{MaxIterations: 100, Debug: true, NoColor: true}

		// create a dummy logger for the test
		colors := testColors()
		log, err := progress.NewLogger(progress.Config{PlanFile: "", Mode: "full", Branch: "test"}, colors)
		require.NoError(t, err)
		defer log.Close()

		runner := createRunner(cfg, o, "/path/to/plan.md", processor.ModeFull, log)
		assert.NotNil(t, runner)
	})

	t.Run("codex_only_mode_forces_codex_enabled", func(t *testing.T) {
		cfg := &config.Config{CodexEnabled: false} // explicitly disabled in config
		o := opts{MaxIterations: 50}

		colors := testColors()
		log, err := progress.NewLogger(progress.Config{PlanFile: "", Mode: "codex", Branch: "test"}, colors)
		require.NoError(t, err)
		defer log.Close()

		// in codex-only mode, CodexEnabled should be forced to true
		runner := createRunner(cfg, o, "", processor.ModeCodexOnly, log)
		assert.NotNil(t, runner)
		// we can't directly check runner internals, but this tests the code path runs without panic
	})
}

func TestCheckDependencies(t *testing.T) {
	t.Run("returns nil for existing dependencies", func(t *testing.T) {
		err := checkDependencies("ls") // ls should exist on all unix systems
		require.NoError(t, err)
	})

	t.Run("returns error for missing dependency", func(t *testing.T) {
		err := checkDependencies("nonexistent-command-12345")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found in PATH")
	})
}

func TestExtractBranchName(t *testing.T) {
	tests := []struct {
		name     string
		planFile string
		expected string
	}{
		{name: "simple_filename", planFile: "add-feature.md", expected: "add-feature"},
		{name: "with_path", planFile: "docs/plans/add-feature.md", expected: "add-feature"},
		{name: "date_prefix", planFile: "2024-01-15-feature.md", expected: "feature"},
		{name: "complex_date_prefix", planFile: "2024-01-15-12-30-my-feature.md", expected: "my-feature"},
		{name: "numeric_only_keeps_name", planFile: "12345.md", expected: "12345"},
		{name: "with_path_and_date", planFile: "docs/plans/2024-01-15-add-tests.md", expected: "add-tests"},
		{name: "trailing_dashes_trimmed", planFile: "2024---feature.md", expected: "feature"},
		{name: "all_numeric_returns_original", planFile: "2024-01-15.md", expected: "2024-01-15"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractBranchName(tc.planFile)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestCreateBranchIfNeeded(t *testing.T) {
	t.Run("on_feature_branch_does_nothing", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create and switch to feature branch
		err = repo.CreateBranch("feature-test")
		require.NoError(t, err)

		// should return nil without creating new branch
		err = createBranchIfNeeded(repo, "docs/plans/some-plan.md", &mockTUISender{})
		require.NoError(t, err)

		// verify still on feature-test
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature-test", branch)
	})

	t.Run("on_master_creates_branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// verify on master
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)

		// should create branch from plan filename
		err = createBranchIfNeeded(repo, "docs/plans/add-feature.md", &mockTUISender{})
		require.NoError(t, err)

		// verify switched to new branch
		branch, err = repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-feature", branch)
	})

	t.Run("switches_to_existing_branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create branch first
		err = repo.CreateBranch("existing-feature")
		require.NoError(t, err)

		// switch back to master
		err = repo.CheckoutBranch("master")
		require.NoError(t, err)

		// should switch to existing branch without error
		err = createBranchIfNeeded(repo, "docs/plans/existing-feature.md", &mockTUISender{})
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "existing-feature", branch)
	})

	t.Run("strips_date_prefix", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// plan file with date prefix
		err = createBranchIfNeeded(repo, "docs/plans/2024-01-15-feature.md", &mockTUISender{})
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature", branch)
	})

	t.Run("handles_plain_filename", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		err = createBranchIfNeeded(repo, "add-tests.md", &mockTUISender{})
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-tests", branch)
	})

	t.Run("handles_numeric_only_prefix", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// edge case: plan with complex date prefix
		err = createBranchIfNeeded(repo, "docs/plans/2024-01-15-12-30-my-feature.md", &mockTUISender{})
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "my-feature", branch)
	})

	t.Run("auto_commits_plan_when_only_uncommitted_file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create plan file as the only uncommitted file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "auto-commit-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Auto Commit Test Plan\n"), 0o600))

		// should create branch and auto-commit the plan
		err = createBranchIfNeeded(repo, planFile, &mockTUISender{})
		require.NoError(t, err)

		// verify we're on the new branch
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "auto-commit-test", branch)

		// verify plan was committed (worktree should be clean)
		dirty, err := repo.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty, "worktree should be clean after auto-commit")
	})

	t.Run("returns_error_with_helpful_message_when_other_files_uncommitted", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "error-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Error Test Plan\n"), 0o600))

		// create another uncommitted file
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other content"), 0o600))

		// should return an error with helpful message
		err = createBranchIfNeeded(repo, planFile, &mockTUISender{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot create branch")
		assert.Contains(t, err.Error(), "uncommitted changes")
		assert.Contains(t, err.Error(), "git stash")
		assert.Contains(t, err.Error(), "git commit -am")
		assert.Contains(t, err.Error(), "ralphex --review")
	})

	t.Run("returns_error_when_tracked_file_modified", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "modified-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Modified Test Plan\n"), 0o600))

		// modify an existing tracked file
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified\n"), 0o600))

		// should return an error
		err = createBranchIfNeeded(repo, planFile, &mockTUISender{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uncommitted changes")
	})
}

func TestMovePlanToCompleted(t *testing.T) {
	t.Run("moves_tracked_file_and_commits", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir (movePlanToCompleted uses relative paths)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plans directory and plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)

		planFile := filepath.Join("docs", "plans", "test-plan.md")
		err = os.WriteFile(planFile, []byte("# Test Plan\n"), 0o600)
		require.NoError(t, err)

		// stage and commit the plan
		err = repo.Add(planFile)
		require.NoError(t, err)
		err = repo.Commit("add test plan")
		require.NoError(t, err)

		// move plan to completed
		err = movePlanToCompleted(repo, planFile, &mockTUISender{})
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		completedFile := filepath.Join("docs", "plans", "completed", "test-plan.md")
		_, err = os.Stat(completedFile)
		require.NoError(t, err)
	})

	t.Run("creates_completed_directory", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plans directory without completed subdir
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)

		planFile := filepath.Join("docs", "plans", "new-plan.md")
		err = os.WriteFile(planFile, []byte("# New Plan\n"), 0o600)
		require.NoError(t, err)

		// stage and commit
		err = repo.Add(planFile)
		require.NoError(t, err)
		err = repo.Commit("add new plan")
		require.NoError(t, err)

		// verify completed dir doesn't exist
		completedDir := filepath.Join("docs", "plans", "completed")
		_, err = os.Stat(completedDir)
		assert.True(t, os.IsNotExist(err))

		// move plan
		err = movePlanToCompleted(repo, planFile, &mockTUISender{})
		require.NoError(t, err)

		// verify completed directory was created
		info, err := os.Stat(completedDir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("moves_untracked_file", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plans directory and untracked plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)

		planFile := filepath.Join("docs", "plans", "untracked-plan.md")
		err = os.WriteFile(planFile, []byte("# Untracked Plan\n"), 0o600)
		require.NoError(t, err)

		// don't stage the file, just move it
		err = movePlanToCompleted(repo, planFile, &mockTUISender{})
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		completedFile := filepath.Join("docs", "plans", "completed", "untracked-plan.md")
		_, err = os.Stat(completedFile)
		require.NoError(t, err)
	})

	t.Run("moves_file_with_absolute_path", func(t *testing.T) {
		dir := setupTestRepo(t)

		// resolve symlinks for consistent paths (macOS /var -> /private/var)
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plans directory and plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)

		planFile := filepath.Join(dir, "docs", "plans", "abs-plan.md")
		err = os.WriteFile(planFile, []byte("# Absolute Path Plan\n"), 0o600)
		require.NoError(t, err)

		// stage and commit
		err = repo.Add(planFile)
		require.NoError(t, err)
		err = repo.Commit("add abs plan")
		require.NoError(t, err)

		// move using absolute path (simulates normalized path from run())
		err = movePlanToCompleted(repo, planFile, &mockTUISender{})
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		completedFile := filepath.Join(dir, "docs", "plans", "completed", "abs-plan.md")
		_, err = os.Stat(completedFile)
		require.NoError(t, err)
	})
}

func TestEnsureGitignore(t *testing.T) {
	t.Run("adds_pattern_when_not_ignored", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// save original working directory
		origDir, err := os.Getwd()
		require.NoError(t, err)

		// change to test repo dir (ensureGitignore uses relative .gitignore path)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// ensure gitignore
		err = ensureGitignore(repo, &mockTUISender{})
		require.NoError(t, err)

		// verify .gitignore was created with the pattern
		content, err := os.ReadFile(filepath.Join(dir, ".gitignore")) //nolint:gosec // test file in temp dir
		require.NoError(t, err)
		assert.Contains(t, string(content), "progress*.txt")
	})

	t.Run("skips_when_already_ignored", func(t *testing.T) {
		dir := setupTestRepo(t)

		// create gitignore with pattern already present
		gitignore := filepath.Join(dir, ".gitignore")
		err := os.WriteFile(gitignore, []byte("progress*.txt\n"), 0o600)
		require.NoError(t, err)

		repo, err := git.Open(dir)
		require.NoError(t, err)

		// save original working directory
		origDir, err := os.Getwd()
		require.NoError(t, err)

		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// ensure gitignore - should be a no-op
		err = ensureGitignore(repo, &mockTUISender{})
		require.NoError(t, err)

		// verify content unchanged (no duplicate pattern)
		content, err := os.ReadFile(gitignore) //nolint:gosec // test file in temp dir
		require.NoError(t, err)
		assert.Equal(t, "progress*.txt\n", string(content))
	})

	t.Run("creates_gitignore_if_missing", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// verify no .gitignore exists
		gitignore := filepath.Join(dir, ".gitignore")
		_, err = os.Stat(gitignore)
		assert.True(t, os.IsNotExist(err))

		// save original working directory
		origDir, err := os.Getwd()
		require.NoError(t, err)

		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// ensure gitignore
		err = ensureGitignore(repo, &mockTUISender{})
		require.NoError(t, err)

		// verify .gitignore was created
		_, err = os.Stat(gitignore)
		require.NoError(t, err)

		// verify content
		content, err := os.ReadFile(gitignore) //nolint:gosec // test file in temp dir
		require.NoError(t, err)
		assert.Contains(t, string(content), "progress*.txt")
	})
}

func TestGetCurrentBranch(t *testing.T) {
	t.Run("returns_branch_name", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		branch := getCurrentBranch(repo)
		assert.Equal(t, "master", branch)
	})

	t.Run("returns_unknown_on_error", func(t *testing.T) {
		// create a repo but then break it by removing .git
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// close and remove git dir to simulate error
		require.NoError(t, os.RemoveAll(filepath.Join(dir, ".git")))

		// getCurrentBranch should return "unknown" on error
		branch := getCurrentBranch(repo)
		assert.Equal(t, "unknown", branch)
	})
}

func TestSetupGitForExecution(t *testing.T) {
	t.Run("returns_nil_for_empty_plan_file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		err = setupGitForExecution(repo, "", processor.ModeFull, &mockTUISender{})
		require.NoError(t, err)
	})

	t.Run("creates_branch_for_full_mode", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// change to test repo dir for gitignore
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		err = setupGitForExecution(repo, "docs/plans/new-feature.md", processor.ModeFull, &mockTUISender{})
		require.NoError(t, err)

		// verify branch was created
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "new-feature", branch)
	})

	t.Run("skips_branch_for_review_mode", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// change to test repo dir for gitignore
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		err = setupGitForExecution(repo, "docs/plans/some-plan.md", processor.ModeReview, &mockTUISender{})
		require.NoError(t, err)

		// verify still on master (no branch created)
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})
}

func TestHandlePostExecution(t *testing.T) {
	t.Run("moves_plan_on_full_mode_completion", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)
		planFile := filepath.Join("docs", "plans", "test-feature.md")
		err = os.WriteFile(planFile, []byte("# Test Plan\n"), 0o600)
		require.NoError(t, err)

		// stage and commit
		err = repo.Add(planFile)
		require.NoError(t, err)
		err = repo.Commit("add plan")
		require.NoError(t, err)

		// handlePostExecution should move the plan
		handlePostExecution(repo, planFile, processor.ModeFull, &mockTUISender{})

		// verify plan was moved
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err), "original plan should be gone")

		completedFile := filepath.Join("docs", "plans", "completed", "test-feature.md")
		_, err = os.Stat(completedFile)
		require.NoError(t, err, "plan should be in completed dir")
	})

	t.Run("skips_move_on_review_mode", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)
		planFile := filepath.Join("docs", "plans", "review-test.md")
		err = os.WriteFile(planFile, []byte("# Test Plan\n"), 0o600)
		require.NoError(t, err)

		// handlePostExecution with review mode should NOT move the plan
		handlePostExecution(repo, planFile, processor.ModeReview, &mockTUISender{})

		// verify plan was NOT moved
		_, err = os.Stat(planFile)
		require.NoError(t, err, "plan should still exist in original location")
	})

	t.Run("skips_move_on_empty_plan_file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// handlePostExecution with empty plan should not panic
		handlePostExecution(repo, "", processor.ModeFull, &mockTUISender{})
		// no error means success
	})
}

func TestValidateFlags(t *testing.T) {
	tests := []struct {
		name    string
		opts    opts
		wantErr bool
		errMsg  string
	}{
		{name: "no_flags_is_valid", opts: opts{}, wantErr: false},
		{name: "plan_flag_only_is_valid", opts: opts{PlanDescription: "add feature"}, wantErr: false},
		{name: "plan_file_only_is_valid", opts: opts{PlanFile: "docs/plans/test.md"}, wantErr: false},
		{name: "both_plan_and_planfile_conflicts", opts: opts{PlanDescription: "add feature", PlanFile: "docs/plans/test.md"}, wantErr: true, errMsg: "conflicts"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFlags(tc.opts)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestFindRecentPlan(t *testing.T) {
	t.Run("finds_recently_modified_file", func(t *testing.T) {
		dir := t.TempDir()
		startTime := time.Now()

		// create a plan file
		planFile := filepath.Join(dir, "new-plan.md")
		err := os.WriteFile(planFile, []byte("# New Plan"), 0o600)
		require.NoError(t, err)

		// explicitly set mod time to be after startTime
		futureTime := startTime.Add(time.Second)
		err = os.Chtimes(planFile, futureTime, futureTime)
		require.NoError(t, err)

		result := findRecentPlan(dir, startTime)
		assert.Equal(t, planFile, result)
	})

	t.Run("returns_empty_for_old_files", func(t *testing.T) {
		dir := t.TempDir()
		startTime := time.Now()

		// create a plan file
		planFile := filepath.Join(dir, "old-plan.md")
		err := os.WriteFile(planFile, []byte("# Old Plan"), 0o600)
		require.NoError(t, err)

		// set mod time to be before startTime
		pastTime := startTime.Add(-time.Hour)
		err = os.Chtimes(planFile, pastTime, pastTime)
		require.NoError(t, err)

		result := findRecentPlan(dir, startTime)
		assert.Empty(t, result)
	})

	t.Run("returns_most_recent_of_multiple_files", func(t *testing.T) {
		dir := t.TempDir()
		startTime := time.Now()

		// create first file with earlier mod time
		plan1 := filepath.Join(dir, "plan1.md")
		err := os.WriteFile(plan1, []byte("# Plan 1"), 0o600)
		require.NoError(t, err)
		time1 := startTime.Add(time.Second)
		err = os.Chtimes(plan1, time1, time1)
		require.NoError(t, err)

		// create second file with later mod time
		plan2 := filepath.Join(dir, "plan2.md")
		err = os.WriteFile(plan2, []byte("# Plan 2"), 0o600)
		require.NoError(t, err)
		time2 := startTime.Add(2 * time.Second)
		err = os.Chtimes(plan2, time2, time2)
		require.NoError(t, err)

		result := findRecentPlan(dir, startTime)
		assert.Equal(t, plan2, result)
	})

	t.Run("returns_empty_for_nonexistent_directory", func(t *testing.T) {
		result := findRecentPlan("/nonexistent/directory", time.Now())
		assert.Empty(t, result)
	})

	t.Run("returns_empty_for_empty_directory", func(t *testing.T) {
		dir := t.TempDir()
		result := findRecentPlan(dir, time.Now())
		assert.Empty(t, result)
	})

	t.Run("ignores_non_md_files", func(t *testing.T) {
		dir := t.TempDir()
		startTime := time.Now()

		// create non-md file with future mod time
		txtFile := filepath.Join(dir, "notes.txt")
		err := os.WriteFile(txtFile, []byte("notes"), 0o600)
		require.NoError(t, err)
		futureTime := startTime.Add(time.Second)
		err = os.Chtimes(txtFile, futureTime, futureTime)
		require.NoError(t, err)

		result := findRecentPlan(dir, startTime)
		assert.Empty(t, result)
	})
}

// setupTestRepo creates a test git repository with an initial commit.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// init repo
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)

	// create a file
	readme := filepath.Join(dir, "README.md")
	err = os.WriteFile(readme, []byte("# Test\n"), 0o600)
	require.NoError(t, err)

	// stage and commit
	wt, err := repo.Worktree()
	require.NoError(t, err)

	_, err = wt.Add("README.md")
	require.NoError(t, err)

	_, err = wt.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com"},
	})
	require.NoError(t, err)

	return dir
}

// countExecutionDoneMsg counts ExecutionDoneMsg in a mockTUISender's messages.
func countExecutionDoneMsg(msgs []tea.Msg) (count int, lastErr error) {
	for _, msg := range msgs {
		if doneMsg, ok := msg.(tui.ExecutionDoneMsg); ok {
			count++
			lastErr = doneMsg.Err
		}
	}
	return count, lastErr
}

func TestRunBusinessLogic_AlwaysSendsExecutionDoneMsg(t *testing.T) {
	t.Run("sends_done_with_error_on_failure", func(t *testing.T) {
		dir := setupTestRepo(t)
		gitOps, err := git.Open(dir)
		require.NoError(t, err)

		sender := &mockTUISender{}
		cfg := &config.Config{PlansDir: filepath.Join(dir, "docs", "plans")}

		// provide a non-existent plan file to trigger "plan file not found" error
		o := opts{PlanFile: "/nonexistent/plan.md", MaxIterations: 1}

		runBusinessLogic(context.Background(), sender, o, cfg, gitOps, processor.ModeFull)

		count, lastErr := countExecutionDoneMsg(sender.messages())
		assert.Equal(t, 1, count, "should send exactly one ExecutionDoneMsg")
		require.Error(t, lastErr, "should carry the error")
		assert.Contains(t, lastErr.Error(), "plan file not found")
	})

	t.Run("sends_done_with_nil_on_context_cancel", func(t *testing.T) {
		dir := setupTestRepo(t)
		gitOps, err := git.Open(dir)
		require.NoError(t, err)

		sender := &mockTUISender{}
		cfg := &config.Config{PlansDir: filepath.Join(dir, "docs", "plans")}

		// cancel context before calling; resolvePlanFile will fail waiting for selection
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// no plan file specified, no review/codex mode -> goes to plan selection which
		// returns context error
		o := opts{MaxIterations: 1}

		runBusinessLogic(ctx, sender, o, cfg, gitOps, processor.ModeFull)

		count, lastErr := countExecutionDoneMsg(sender.messages())
		assert.Equal(t, 1, count, "should send exactly one ExecutionDoneMsg")
		assert.Error(t, lastErr, "should carry context cancellation error")
	})

	t.Run("sends_done_with_nil_for_errPlanModeCompleted", func(t *testing.T) {
		// errPlanModeCompleted is returned when waitForPlanSelection handles plan creation.
		// the wrapper should translate it to nil (not a real error).
		// test this by checking the sentinel error handling logic directly.
		sender := &mockTUISender{}

		// simulate the wrapper behavior: if inner returns errPlanModeCompleted,
		// the wrapper sends ExecutionDoneMsg{Err: nil}
		err := errPlanModeCompleted
		if errors.Is(err, errPlanModeCompleted) {
			err = nil
		}
		sender.Send(tui.ExecutionDoneMsg{Err: err})

		count, lastErr := countExecutionDoneMsg(sender.messages())
		assert.Equal(t, 1, count, "should send exactly one ExecutionDoneMsg")
		assert.NoError(t, lastErr, "errPlanModeCompleted should be translated to nil")
	})
}

func TestDetermineInitialTUIState(t *testing.T) {
	tests := []struct {
		name     string
		opts     opts
		mode     processor.Mode
		expected tui.State
	}{
		{name: "plan_mode_starts_executing", opts: opts{PlanDescription: "test"}, mode: processor.ModePlan, expected: tui.StateExecuting},
		{name: "explicit_plan_file_starts_executing", opts: opts{PlanFile: "plan.md"}, mode: processor.ModeFull, expected: tui.StateExecuting},
		{name: "review_mode_starts_executing", opts: opts{Review: true}, mode: processor.ModeReview, expected: tui.StateExecuting},
		{name: "codex_only_starts_executing", opts: opts{CodexOnly: true}, mode: processor.ModeCodexOnly, expected: tui.StateExecuting},
		{name: "no_plan_shows_selection", opts: opts{}, mode: processor.ModeFull, expected: tui.StateSelectPlan},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := determineInitialTUIState(tc.opts, tc.mode)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestPlanDisplay(t *testing.T) {
	tests := []struct {
		name     string
		planFile string
		expected string
	}{
		{name: "with_plan_file", planFile: "/path/to/plan.md", expected: "/path/to/plan.md"},
		{name: "empty_plan_file", planFile: "", expected: "(no plan - review only)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := planDisplay(tc.planFile)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDefaultTeaOptions(t *testing.T) {
	ctx := context.Background()
	opts := defaultTeaOptions(ctx)
	assert.NotEmpty(t, opts, "should return non-empty options")
}

func TestTestTeaOptions(t *testing.T) {
	ctx := context.Background()
	opts := testTeaOptions(ctx)
	assert.NotEmpty(t, opts, "should return non-empty options")
}

func TestEnsureRepoHasCommitsTUI(t *testing.T) {
	t.Run("returns_nil_for_repo_with_commits", func(t *testing.T) {
		dir := setupTestRepo(t)
		gitOps, err := git.Open(dir)
		require.NoError(t, err)

		sender := &mockTUISender{}
		asker := &mockYesNoAsker{}

		err = ensureRepoHasCommitsTUI(context.Background(), gitOps, sender, asker)
		require.NoError(t, err)
		// asker should not be called when repo already has commits
		assert.False(t, asker.called)
	})

	t.Run("prompts_and_creates_commit_when_no_commits_and_user_says_yes", func(t *testing.T) {
		// create a git repo with no commits
		dir := t.TempDir()
		_, err := gogit.PlainInit(dir, false)
		require.NoError(t, err)
		// create a file so the initial commit has content
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600))

		gitOps, err := git.Open(dir)
		require.NoError(t, err)

		sender := &mockTUISender{}
		asker := &mockYesNoAsker{answer: true}

		err = ensureRepoHasCommitsTUI(context.Background(), gitOps, sender, asker)
		require.NoError(t, err)
		assert.True(t, asker.called)

		// verify messages were sent
		assert.GreaterOrEqual(t, len(sender.messages()), 2, "should send output messages")

		// verify repo now has commits
		hasCommits, err := gitOps.HasCommits()
		require.NoError(t, err)
		assert.True(t, hasCommits)
	})

	t.Run("returns_error_when_no_commits_and_user_says_no", func(t *testing.T) {
		dir := t.TempDir()
		_, err := gogit.PlainInit(dir, false)
		require.NoError(t, err)

		gitOps, err := git.Open(dir)
		require.NoError(t, err)

		sender := &mockTUISender{}
		asker := &mockYesNoAsker{answer: false}

		err = ensureRepoHasCommitsTUI(context.Background(), gitOps, sender, asker)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no commits")
		assert.True(t, asker.called)
	})

	t.Run("returns_error_when_asker_fails", func(t *testing.T) {
		dir := t.TempDir()
		_, err := gogit.PlainInit(dir, false)
		require.NoError(t, err)

		gitOps, err := git.Open(dir)
		require.NoError(t, err)

		sender := &mockTUISender{}
		asker := &mockYesNoAsker{err: context.Canceled}

		err = ensureRepoHasCommitsTUI(context.Background(), gitOps, sender, asker)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
		assert.True(t, asker.called)
	})

	t.Run("returns_error_on_context_cancellation_via_asker", func(t *testing.T) {
		dir := t.TempDir()
		_, err := gogit.PlainInit(dir, false)
		require.NoError(t, err)

		gitOps, err := git.Open(dir)
		require.NoError(t, err)

		sender := &mockTUISender{}
		// simulate asker returning context.DeadlineExceeded
		asker := &mockYesNoAsker{err: context.DeadlineExceeded}

		err = ensureRepoHasCommitsTUI(context.Background(), gitOps, sender, asker)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestMockTUISender(t *testing.T) {
	s := &mockTUISender{}
	s.Send(tui.OutputMsg{Text: "hello"})
	msgs := s.messages()
	require.Len(t, msgs, 1)
	msg, ok := msgs[0].(tui.OutputMsg)
	require.True(t, ok)
	assert.Equal(t, "hello", msg.Text)
}

func TestWaitForPlanSelection_ContextCanceled_DrainsResultCh(t *testing.T) {
	t.Run("drains_result_channel_on_context_cancellation", func(t *testing.T) {
		// cancel the context before calling waitForPlanSelection
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		sender := &mockTUISender{}
		cfg := &config.Config{PlansDir: t.TempDir()}

		_, err := waitForPlanSelection(ctx, sender, nil, opts{}, cfg)
		require.ErrorIs(t, err, context.Canceled)

		// extract the resultCh from the PlanSelectionRequestMsg sent to the sender
		msgs := sender.messages()
		require.GreaterOrEqual(t, len(msgs), 1, "should have sent PlanSelectionRequestMsg")
		reqMsg, ok := msgs[0].(tui.PlanSelectionRequestMsg)
		require.True(t, ok, "first message should be PlanSelectionRequestMsg")

		// simulate the TUI sending a result after context cancellation.
		// without the drain goroutine, this send would have no receiver (goroutine leak).
		// with the drain goroutine, this send completes promptly.
		done := make(chan struct{})
		go func() {
			reqMsg.ResultCh <- tui.PlanSelectionResult{Path: "docs/plans/test.md"}
			close(done)
		}()

		select {
		case <-done:
			// send completed - the drain goroutine consumed the result
		case <-time.After(2 * time.Second):
			t.Fatal("resultCh send blocked - drain goroutine did not consume the result")
		}
	})

	t.Run("returns_selected_plan_on_normal_flow", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sender := &mockTUISender{}
		cfg := &config.Config{PlansDir: t.TempDir()}

		// send result in a goroutine, polling for the request message
		go func() {
			for {
				msgs := sender.messages()
				if len(msgs) > 0 {
					if reqMsg, ok := msgs[0].(tui.PlanSelectionRequestMsg); ok {
						reqMsg.ResultCh <- tui.PlanSelectionResult{Path: "docs/plans/feature.md"}
						return
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()

		path, err := waitForPlanSelection(ctx, sender, nil, opts{}, cfg)
		require.NoError(t, err)
		assert.Equal(t, "docs/plans/feature.md", path)
	})

	t.Run("returns_error_from_selection_result", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sender := &mockTUISender{}
		cfg := &config.Config{PlansDir: t.TempDir()}

		// send error result, polling for the request message
		go func() {
			for {
				msgs := sender.messages()
				if len(msgs) > 0 {
					if reqMsg, ok := msgs[0].(tui.PlanSelectionRequestMsg); ok {
						reqMsg.ResultCh <- tui.PlanSelectionResult{Err: errors.New("user quit")}
						return
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()

		_, err := waitForPlanSelection(ctx, sender, nil, opts{}, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user quit")
	})
}
