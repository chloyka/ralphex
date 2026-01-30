// Package main provides ralphex - autonomous plan execution with Claude Code.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jessevdk/go-flags"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/git"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
	"github.com/umputun/ralphex/pkg/tui"
	"github.com/umputun/ralphex/pkg/web"
)

// opts holds all command-line options.
type opts struct {
	MaxIterations   int      `short:"m" long:"max-iterations" default:"50" description:"maximum task iterations"`
	Review          bool     `short:"r" long:"review" description:"skip task execution, run full review pipeline"`
	CodexOnly       bool     `short:"c" long:"codex-only" description:"skip tasks and first review, run only codex loop"`
	PlanDescription string   `long:"plan" description:"create plan interactively (enter plan description)"`
	Debug           bool     `short:"d" long:"debug" description:"enable debug logging"`
	NoColor         bool     `long:"no-color" description:"disable color output"`
	Version         bool     `short:"v" long:"version" description:"print version and exit"`
	Serve           bool     `short:"s" long:"serve" description:"start web dashboard for real-time streaming"`
	Port            int      `short:"p" long:"port" default:"8080" description:"web dashboard port"`
	Watch           []string `short:"w" long:"watch" description:"directories to watch for progress files (repeatable)"`
	Reset           bool     `long:"reset" description:"interactively reset global config to embedded defaults"`

	PlanFile string `positional-arg-name:"plan-file" description:"path to plan file (optional, interactive selection if omitted)"`

	// teaOptions allows tests to override tea.Program options (e.g., to avoid TTY requirement).
	// not set via CLI flags — only used in tests.
	teaOptions []tea.ProgramOption
}

var revision = "unknown"

// errPlanModeCompleted is a sentinel error indicating plan mode ran to completion
// inside waitForPlanSelection (ExecutionDoneMsg already sent to TUI).
var errPlanModeCompleted = errors.New("plan mode completed")

// datePrefixRe matches date-like prefixes in plan filenames (e.g., "2024-01-15-").
var datePrefixRe = regexp.MustCompile(`^[\d-]+`)

// executePlanRequest holds parameters for plan execution.
type executePlanRequest struct {
	PlanFile string
	Mode     processor.Mode
	GitOps   *git.Repo
	Config   *config.Config
}

// webDashboardParams holds parameters for web dashboard setup.
type webDashboardParams struct {
	BaseLog         processor.Logger
	Port            int
	PlanFile        string
	Branch          string
	WatchDirs       []string   // CLI watch dirs
	ConfigWatchDirs []string   // config watch dirs
	Sender          tui.Sender // TUI message sender for output routing
}

func main() {
	fmt.Printf("ralphex %s\n", revision)

	var o opts
	parser := flags.NewParser(&o, flags.Default)
	parser.Usage = "[OPTIONS] [plan-file]"

	args, err := parser.Parse()
	if err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if o.Version {
		os.Exit(0)
	}

	// handle positional argument
	if len(args) > 0 {
		o.PlanFile = args[0]
	}

	// setup context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, o); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o opts) error {
	// validate conflicting flags
	if err := validateFlags(o); err != nil {
		return err
	}

	// handle --reset flag early (before full config load)
	// reset completes, then continues with normal execution if other args provided
	if o.Reset {
		if err := runReset(); err != nil {
			return err
		}
		// if reset was the only operation, exit successfully
		if isResetOnly(o) {
			return nil
		}
	}

	// load config first to get custom command paths
	cfg, err := config.Load("") // empty string uses default location
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// validate color config (all colors guaranteed populated via fallback)
	progress.NewColors(cfg.Colors)

	// watch-only mode: --serve with watch dirs (CLI or config) and no plan file
	// runs web dashboard without plan execution, can run from any directory
	if isWatchOnlyMode(o, cfg.WatchDirs) {
		return runWatchOnly(ctx, o, cfg)
	}

	// check dependencies using configured command (or default "claude")
	if depErr := checkClaudeDep(cfg); depErr != nil {
		return depErr
	}

	// require running from repo root
	if _, statErr := os.Stat(".git"); statErr != nil {
		return errors.New("must run from repository root (no .git directory found)")
	}

	// open git repository
	gitOps, err := git.Open(".")
	if err != nil {
		return fmt.Errorf("open git repo: %w", err)
	}

	mode := determineMode(o)

	// determine initial TUI state based on mode
	initialState := determineInitialTUIState(o, mode)

	// create TUI model with viewport configuration
	vpCfg := tui.NewViewportConfig(revision, getCurrentBranch(gitOps), "")
	model := tui.NewModelWithConfig(initialState, vpCfg)

	// set plans directory for plan selection state
	if initialState == tui.StateSelectPlan {
		model = model.WithPlansDir(cfg.PlansDir)
	}

	// build tea.Program options
	teaOpts := defaultTeaOptions(ctx)
	if len(o.teaOptions) > 0 {
		teaOpts = o.teaOptions
	}

	// create and run tea.Program
	p := tea.NewProgram(model, teaOpts...)

	// create a safe sender that becomes a no-op after TUI exits,
	// preventing runBusinessLogic from blocking on p.Send() after quit.
	safeSender := tui.NewSafeSender(p)

	// run business logic in background goroutine, sending results to TUI
	go runBusinessLogic(ctx, safeSender, o, cfg, gitOps, mode)

	// run the TUI - blocks until quit
	finalModel, err := p.Run()

	// stop the safe sender so any pending p.Send() calls become no-ops
	safeSender.Stop()

	if err != nil {
		return fmt.Errorf("TUI: %w", err)
	}

	// extract result from final model
	if m, ok := finalModel.(tui.Model); ok {
		if err := m.Result(); err != nil {
			return fmt.Errorf("execution: %w", err)
		}
	}
	return nil
}

// defaultTeaOptions returns the default tea.Program options for normal (non-test) execution.
func defaultTeaOptions(ctx context.Context) []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
}

// testTeaOptions returns tea.Program options suitable for testing (no TTY required).
func testTeaOptions(ctx context.Context) []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithContext(ctx)}
}

// determineInitialTUIState returns the initial TUI state based on options and mode.
func determineInitialTUIState(o opts, mode processor.Mode) tui.State {
	switch {
	case mode == processor.ModePlan:
		// plan mode with description starts executing immediately
		return tui.StateExecuting
	case o.PlanFile != "":
		// explicit plan file - skip selection, go to executing
		return tui.StateExecuting
	case o.Review || o.CodexOnly:
		// review/codex modes don't need plan selection
		return tui.StateExecuting
	default:
		// no plan specified - show plan selection
		return tui.StateSelectPlan
	}
}

// runBusinessLogic runs the main execution flow in a background goroutine,
// communicating with the TUI via a Sender (safe for use after TUI exits).
// always sends ExecutionDoneMsg when done, regardless of outcome.
func runBusinessLogic(ctx context.Context, sender tui.Sender, o opts, cfg *config.Config, gitOps *git.Repo, mode processor.Mode) {
	err := runBusinessLogicInner(ctx, sender, o, cfg, gitOps, mode)
	// always notify TUI that execution is done. uses safeSender so this
	// is a no-op if the TUI already quit (e.g., user pressed Ctrl+C).
	// errPlanModeCompleted is a sentinel — not a real error, report nil.
	if errors.Is(err, errPlanModeCompleted) {
		err = nil
	}
	sender.Send(tui.ExecutionDoneMsg{Err: err})
}

// yesNoAsker is a consumer-side interface for asking yes/no questions via the TUI.
type yesNoAsker interface {
	AskYesNo(ctx context.Context, question string) (bool, error)
}

// runBusinessLogicInner contains the main business logic flow.
func runBusinessLogicInner(ctx context.Context, sender tui.Sender, o opts, cfg *config.Config,
	gitOps *git.Repo, mode processor.Mode) error {
	// create collector early so it's available for ensureRepoHasCommitsTUI and plan mode
	collector := tui.NewCollector(sender)

	// ensure repository has commits (prompts via TUI)
	if ensureErr := ensureRepoHasCommitsTUI(ctx, gitOps, sender, collector); ensureErr != nil {
		return ensureErr
	}

	// plan mode has different flow - doesn't require plan file selection
	if mode == processor.ModePlan {
		return runPlanModeTUI(ctx, sender, o, executePlanRequest{
			Mode:   processor.ModePlan,
			GitOps: gitOps,
			Config: cfg,
		})
	}

	// determine plan file
	planFile, err := resolvePlanFile(ctx, sender, o, cfg, gitOps)
	if err != nil {
		return err
	}

	if setupErr := setupGitForExecution(gitOps, planFile, mode, sender); setupErr != nil {
		return setupErr
	}

	return executePlanTUI(ctx, sender, o, executePlanRequest{
		PlanFile: planFile,
		Mode:     mode,
		GitOps:   gitOps,
		Config:   cfg,
	})
}

// resolvePlanFile determines the plan file path, either from CLI args or TUI selection.
func resolvePlanFile(ctx context.Context, sender tui.Sender, o opts, cfg *config.Config,
	gitOps *git.Repo) (string, error) {
	// if plan file explicitly provided, validate and return
	if o.PlanFile != "" {
		if _, err := os.Stat(o.PlanFile); err != nil {
			return "", fmt.Errorf("plan file not found: %s", o.PlanFile)
		}
		abs, err := filepath.Abs(o.PlanFile)
		if err != nil {
			return "", fmt.Errorf("resolve plan path: %w", err)
		}
		return abs, nil
	}

	// for review-only modes, plan is optional
	if o.Review || o.CodexOnly {
		return "", nil
	}

	// wait for TUI plan selection (the TUI is already showing plan list)
	planFile, err := waitForPlanSelection(ctx, sender, gitOps, o, cfg)
	if err != nil {
		return "", err
	}
	if planFile == "" {
		return "", errors.New("plan file required for task execution")
	}

	abs, err := filepath.Abs(planFile)
	if err != nil {
		return "", fmt.Errorf("resolve plan path: %w", err)
	}
	return abs, nil
}

// waitForPlanSelection waits for the user to select a plan in the TUI.
// the TUI is already showing the plan list; this function blocks until selection.
func waitForPlanSelection(ctx context.Context, sender tui.Sender,
	gitOps *git.Repo, o opts, cfg *config.Config) (string, error) {
	// register a result channel with the TUI
	resultCh := make(chan tui.PlanSelectionResult, 1)
	sender.Send(tui.PlanSelectionRequestMsg{ResultCh: resultCh})

	select {
	case <-ctx.Done():
		// drain resultCh so the TUI goroutine producing the result does not leak.
		// the channel is buffered(1), but draining ensures the producer is unblocked
		// even if timing causes the send after context cancellation.
		go func() { <-resultCh }()
		return "", fmt.Errorf("wait for plan selection: %w", ctx.Err())
	case result := <-resultCh:
		if result.Err != nil {
			return "", result.Err
		}
		// if a plan description was created instead of selection, switch to plan mode
		if result.Description != "" {
			o.PlanDescription = result.Description
			if err := runPlanModeTUI(ctx, sender, o, executePlanRequest{
				Mode:   processor.ModePlan,
				GitOps: gitOps,
				Config: cfg,
			}); err != nil {
				return "", err
			}
			// plan mode completed successfully; errPlanModeCompleted tells the wrapper to send ExecutionDoneMsg{Err: nil}
			return "", errPlanModeCompleted
		}
		return result.Path, nil
	}
}

// ensureRepoHasCommitsTUI checks that the repo has commits, prompting via TUI if needed.
// uses the collector's AskYesNo for safe question/answer flow with proper context cancellation.
func ensureRepoHasCommitsTUI(ctx context.Context, gitOps *git.Repo, sender tui.Sender, asker yesNoAsker) error {
	hasCommits, err := gitOps.HasCommits()
	if err != nil {
		return fmt.Errorf("check commits: %w", err)
	}
	if hasCommits {
		return nil
	}

	// prompt user via TUI
	sender.Send(tui.OutputMsg{Text: "repository has no commits"})
	sender.Send(tui.OutputMsg{Text: "ralphex needs at least one commit to create feature branches."})

	yes, askErr := asker.AskYesNo(ctx, "create initial commit?")
	if askErr != nil {
		return fmt.Errorf("create initial commit: %w", askErr)
	}
	if !yes {
		return errors.New("no commits - please create initial commit manually")
	}

	if err := gitOps.CreateInitialCommit("initial commit"); err != nil {
		return fmt.Errorf("create initial commit: %w", err)
	}
	sender.Send(tui.OutputMsg{Text: "created initial commit"})
	return nil
}

// executePlanTUI runs the main execution loop using the TUI for output.
// creates a tui.teaLogger, wires it as processor.Logger, and runs the execution in the TUI.
func executePlanTUI(ctx context.Context, sender tui.Sender, o opts, req executePlanRequest) error {
	branch := getCurrentBranch(req.GitOps)

	// create TUI logger (writes to progress file + sends messages to TUI)
	tuiLog, err := tui.NewLogger(tui.LoggerConfig{
		PlanFile: req.PlanFile,
		Mode:     string(req.Mode),
		Branch:   branch,
	}, sender)
	if err != nil {
		return fmt.Errorf("create TUI logger: %w", err)
	}
	tuiLogClosed := false
	defer func() {
		if tuiLogClosed {
			return
		}
		if closeErr := tuiLog.Close(); closeErr != nil {
			sender.Send(tui.OutputMsg{Text: fmt.Sprintf("warning: failed to close progress log: %v", closeErr)})
		}
	}()

	// send startup info to TUI for header display
	sender.Send(tui.StartupInfoMsg{
		PlanFile: req.PlanFile,
		Branch:   branch,
	})

	// wrap logger with broadcast logger if --serve is enabled
	var runnerLog processor.Logger = tuiLog
	if o.Serve {
		broadcastLog, webErr := startWebDashboard(ctx, webDashboardParams{
			BaseLog:         tuiLog,
			Port:            o.Port,
			PlanFile:        req.PlanFile,
			Branch:          branch,
			WatchDirs:       o.Watch,
			ConfigWatchDirs: req.Config.WatchDirs,
			Sender:          sender,
		})
		if webErr != nil {
			return webErr
		}
		runnerLog = broadcastLog
	}

	// log startup info through the logger
	tuiLog.Print("starting ralphex loop: %s (max %d iterations)", planDisplay(req.PlanFile), o.MaxIterations)
	tuiLog.Print("branch: %s", branch)
	tuiLog.Print("progress log: %s", tuiLog.Path())

	// create and run the runner
	r := createRunner(req.Config, o, req.PlanFile, req.Mode, runnerLog)
	if runErr := r.Run(ctx); runErr != nil {
		return fmt.Errorf("runner: %w", runErr)
	}

	// handle post-execution tasks
	handlePostExecution(req.GitOps, req.PlanFile, req.Mode, sender)

	elapsed := tuiLog.Elapsed()
	tuiLog.Print("completed in %s", elapsed)

	// keep web dashboard running after execution completes
	if o.Serve {
		sender.Send(tui.OutputMsg{Text: fmt.Sprintf("web dashboard still running at http://localhost:%d (press Ctrl+C to exit)", o.Port)})
		if err := tuiLog.Close(); err != nil {
			sender.Send(tui.OutputMsg{Text: fmt.Sprintf("warning: failed to close progress log: %v", err)})
		}
		tuiLogClosed = true
		<-ctx.Done()
		return nil
	}

	return nil
}

// runPlanModeTUI executes interactive plan creation mode using TUI for I/O.
func runPlanModeTUI(ctx context.Context, sender tui.Sender, o opts, req executePlanRequest) error {
	// ensure gitignore has progress files
	if gitignoreErr := ensureGitignore(req.GitOps, sender); gitignoreErr != nil {
		return gitignoreErr
	}

	branch := getCurrentBranch(req.GitOps)

	// create TUI logger for plan mode
	tuiLog, err := tui.NewLogger(tui.LoggerConfig{
		PlanDescription: o.PlanDescription,
		Mode:            string(processor.ModePlan),
		Branch:          branch,
	}, sender)
	if err != nil {
		return fmt.Errorf("create TUI logger: %w", err)
	}
	defer func() {
		if closeErr := tuiLog.Close(); closeErr != nil {
			sender.Send(tui.OutputMsg{Text: fmt.Sprintf("warning: failed to close progress log: %v", closeErr)})
		}
	}()

	// send startup info
	sender.Send(tui.StartupInfoMsg{
		Branch: branch,
	})

	// log startup info
	tuiLog.Print("starting interactive plan creation")
	tuiLog.Print("request: %s", o.PlanDescription)
	tuiLog.Print("branch: %s (max %d iterations)", branch, o.MaxIterations)
	tuiLog.Print("progress log: %s", tuiLog.Path())

	// create TUI input collector
	collector := tui.NewCollector(sender)

	// record start time for finding the created plan
	startTime := time.Now()

	// create and configure runner
	r := processor.New(processor.Config{
		PlanDescription:  o.PlanDescription,
		ProgressPath:     tuiLog.Path(),
		Mode:             processor.ModePlan,
		MaxIterations:    o.MaxIterations,
		Debug:            o.Debug,
		NoColor:          o.NoColor,
		IterationDelayMs: req.Config.IterationDelayMs,
		AppConfig:        req.Config,
	}, tuiLog)
	r.SetInputCollector(collector)

	// run the plan creation loop
	if runErr := r.Run(ctx); runErr != nil {
		return fmt.Errorf("plan creation: %w", runErr)
	}

	// find the newly created plan file
	planFile := findRecentPlan(req.Config.PlansDir, startTime)
	elapsed := tuiLog.Elapsed()

	if planFile != "" {
		relPath, relErr := filepath.Rel(".", planFile)
		if relErr != nil {
			relPath = planFile
		}
		tuiLog.Print("plan creation completed in %s, created %s", elapsed, relPath)
	} else {
		tuiLog.Print("plan creation completed in %s", elapsed)
	}

	// if no plan file found, can't continue to implementation
	if planFile == "" {
		return nil
	}

	// ask user if they want to continue with plan implementation
	answer, askErr := collector.AskQuestion(ctx, "Continue with plan implementation?",
		[]string{"Yes, execute plan", "No, exit"})
	if askErr != nil {
		if ctx.Err() == nil {
			sender.Send(tui.OutputMsg{Text: fmt.Sprintf("warning: input error: %v", askErr)})
		}
		return nil
	}

	if !strings.HasPrefix(answer, "Yes") {
		return nil
	}

	tuiLog.Print("continuing with plan implementation...")

	// create branch if needed
	if branchErr := createBranchIfNeeded(req.GitOps, planFile, sender); branchErr != nil {
		return branchErr
	}

	return executePlanTUI(ctx, sender, o, executePlanRequest{
		PlanFile: planFile,
		Mode:     processor.ModeFull,
		GitOps:   req.GitOps,
		Config:   req.Config,
	})
}

// planDisplay returns a display string for the plan file.
func planDisplay(planFile string) string {
	if planFile == "" {
		return "(no plan - review only)"
	}
	return planFile
}

// getCurrentBranch returns the current git branch name or "unknown" if unavailable.
func getCurrentBranch(gitOps *git.Repo) string {
	branch, err := gitOps.CurrentBranch()
	if err != nil || branch == "" {
		return "unknown"
	}
	return branch
}

// handlePostExecution handles tasks after runner completion.
func handlePostExecution(gitOps *git.Repo, planFile string, mode processor.Mode, sender tui.Sender) {
	// move completed plan to completed/ directory
	if planFile != "" && mode == processor.ModeFull {
		if moveErr := movePlanToCompleted(gitOps, planFile, sender); moveErr != nil {
			sender.Send(tui.OutputMsg{Text: fmt.Sprintf("warning: failed to move plan to completed: %v", moveErr)})
		}
	}
}

// setupGitForExecution prepares git state for execution (branch, gitignore).
func setupGitForExecution(gitOps *git.Repo, planFile string, mode processor.Mode, sender tui.Sender) error {
	if planFile == "" {
		return nil
	}
	if mode == processor.ModeFull {
		if err := createBranchIfNeeded(gitOps, planFile, sender); err != nil {
			return err
		}
	}
	return ensureGitignore(gitOps, sender)
}

// checkClaudeDep checks that the claude command is available in PATH.
func checkClaudeDep(cfg *config.Config) error {
	claudeCmd := cfg.ClaudeCommand
	if claudeCmd == "" {
		claudeCmd = "claude"
	}
	return checkDependencies(claudeCmd)
}

// isWatchOnlyMode returns true if running in watch-only mode.
// watch-only mode runs the web dashboard without executing any plan.
//
// enabled when all conditions are met:
//   - --serve flag is set
//   - no plan file provided (neither positional arg nor --plan)
//   - watch directories exist (via --watch flag or config file)
//
// use cases:
//   - monitoring multiple concurrent ralphex executions from a central dashboard
//   - viewing progress of ralphex sessions running in other terminals
//
// example: ralphex --serve --watch ~/projects --watch /tmp
func isWatchOnlyMode(o opts, configWatchDirs []string) bool {
	return o.Serve && o.PlanFile == "" && o.PlanDescription == "" && (len(o.Watch) > 0 || len(configWatchDirs) > 0)
}

// determineMode returns the execution mode based on CLI flags.
func determineMode(o opts) processor.Mode {
	switch {
	case o.PlanDescription != "":
		return processor.ModePlan
	case o.CodexOnly:
		return processor.ModeCodexOnly
	case o.Review:
		return processor.ModeReview
	default:
		return processor.ModeFull
	}
}

// validateFlags checks for conflicting CLI flags.
func validateFlags(o opts) error {
	if o.PlanDescription != "" && o.PlanFile != "" {
		return errors.New("--plan flag conflicts with plan file argument; use one or the other")
	}
	return nil
}

// createRunner creates a processor.Runner with the given configuration.
func createRunner(cfg *config.Config, o opts, planFile string, mode processor.Mode, log processor.Logger) *processor.Runner {
	// --codex-only mode forces codex enabled regardless of config
	codexEnabled := cfg.CodexEnabled
	if mode == processor.ModeCodexOnly {
		codexEnabled = true
	}
	return processor.New(processor.Config{
		PlanFile:         planFile,
		ProgressPath:     log.Path(),
		Mode:             mode,
		MaxIterations:    o.MaxIterations,
		Debug:            o.Debug,
		NoColor:          o.NoColor,
		IterationDelayMs: cfg.IterationDelayMs,
		TaskRetryCount:   cfg.TaskRetryCount,
		CodexEnabled:     codexEnabled,
		AppConfig:        cfg,
	}, log)
}

// extractBranchName derives a branch name from a plan file path.
// removes the .md extension and strips any leading date prefix (e.g., "2024-01-15-").
func extractBranchName(planFile string) string {
	name := strings.TrimSuffix(filepath.Base(planFile), ".md")
	branchName := strings.TrimLeft(datePrefixRe.ReplaceAllString(name, ""), "-")
	if branchName == "" {
		return name
	}
	return branchName
}

func createBranchIfNeeded(gitOps *git.Repo, planFile string, sender tui.Sender) error {
	currentBranch, err := gitOps.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	if currentBranch != "main" && currentBranch != "master" {
		return nil // already on feature branch
	}

	branchName := extractBranchName(planFile)

	// check for uncommitted changes to files other than the plan
	hasOtherChanges, err := gitOps.HasChangesOtherThan(planFile)
	if err != nil {
		return fmt.Errorf("check uncommitted files: %w", err)
	}

	if hasOtherChanges {
		// other files have uncommitted changes - show helpful error
		return fmt.Errorf("cannot create branch %q: worktree has uncommitted changes\n\n"+
			"ralphex needs to create a feature branch from %s to isolate plan work.\n\n"+
			"options:\n"+
			"  git stash && ralphex %s && git stash pop   # stash changes temporarily\n"+
			"  git commit -am \"wip\"                       # commit changes first\n"+
			"  ralphex --review                           # skip branch creation (review-only mode)",
			branchName, currentBranch, planFile)
	}

	// check if plan file needs to be committed (untracked, modified, or staged)
	planHasChanges, err := gitOps.FileHasChanges(planFile)
	if err != nil {
		return fmt.Errorf("check plan file status: %w", err)
	}

	// create or switch to branch
	if gitOps.BranchExists(branchName) {
		sender.Send(tui.OutputMsg{Text: "switching to existing branch: " + branchName})
		if err := gitOps.CheckoutBranch(branchName); err != nil {
			return fmt.Errorf("checkout branch %s: %w", branchName, err)
		}
	} else {
		sender.Send(tui.OutputMsg{Text: "creating branch: " + branchName})
		if err := gitOps.CreateBranch(branchName); err != nil {
			return fmt.Errorf("create branch %s: %w", branchName, err)
		}
	}

	// auto-commit plan file if it was the only uncommitted file
	if planHasChanges {
		sender.Send(tui.OutputMsg{Text: "committing plan file: " + filepath.Base(planFile)})
		if err := gitOps.Add(planFile); err != nil {
			return fmt.Errorf("stage plan file: %w", err)
		}
		if err := gitOps.Commit("add plan: " + branchName); err != nil {
			return fmt.Errorf("commit plan file: %w", err)
		}
	}

	return nil
}

func movePlanToCompleted(gitOps *git.Repo, planFile string, sender tui.Sender) error {
	// create completed directory
	completedDir := filepath.Join(filepath.Dir(planFile), "completed")
	if err := os.MkdirAll(completedDir, 0o750); err != nil {
		return fmt.Errorf("create completed dir: %w", err)
	}

	// destination path
	destPath := filepath.Join(completedDir, filepath.Base(planFile))

	// use git mv
	if err := gitOps.MoveFile(planFile, destPath); err != nil {
		// fallback to regular move for untracked files
		if renameErr := os.Rename(planFile, destPath); renameErr != nil {
			return fmt.Errorf("move plan: %w", renameErr)
		}
		// stage the new location - log if fails but continue
		if addErr := gitOps.Add(destPath); addErr != nil {
			sender.Send(tui.OutputMsg{Text: fmt.Sprintf("warning: failed to stage moved plan: %v", addErr)})
		}
	}

	// commit the move
	commitMsg := "move completed plan: " + filepath.Base(planFile)
	if err := gitOps.Commit(commitMsg); err != nil {
		return fmt.Errorf("commit plan move: %w", err)
	}

	sender.Send(tui.OutputMsg{Text: "moved plan to " + destPath})
	return nil
}

func ensureGitignore(gitOps *git.Repo, sender tui.Sender) error {
	// check if already ignored
	ignored, err := gitOps.IsIgnored("progress-test.txt")
	if err == nil && ignored {
		return nil // already ignored
	}

	// write to .gitignore at repo root (not CWD)
	gitignorePath := filepath.Join(gitOps.Root(), ".gitignore")
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // .gitignore needs world-readable
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}

	if _, err := f.WriteString("\n# ralphex progress logs\nprogress*.txt\n"); err != nil {
		f.Close()
		return fmt.Errorf("write .gitignore: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close .gitignore: %w", err)
	}

	sender.Send(tui.OutputMsg{Text: "added progress*.txt to .gitignore"})
	return nil
}

func checkDependencies(deps ...string) error {
	for _, dep := range deps {
		if _, err := exec.LookPath(dep); err != nil {
			return fmt.Errorf("%s not found in PATH", dep)
		}
	}
	return nil
}

// runWatchOnly runs the web dashboard in watch-only mode without plan execution.
// monitors directories for progress files and serves the multi-session dashboard.
func runWatchOnly(ctx context.Context, o opts, cfg *config.Config) error {
	dirs := web.ResolveWatchDirs(o.Watch, cfg.WatchDirs)

	// fail fast if no watch directories configured
	if len(dirs) == 0 {
		return errors.New("no watch directories configured")
	}

	// setup server and watcher
	srvErrCh, watchErrCh, err := setupWatchMode(ctx, o.Port, dirs)
	if err != nil {
		return err
	}

	// print startup info
	printWatchModeInfo(dirs, o.Port)

	// monitor for errors until shutdown
	return monitorWatchMode(ctx, srvErrCh, watchErrCh)
}

// setupWatchMode creates and starts the web server and file watcher for watch-only mode.
// returns error channels for monitoring both components.
func setupWatchMode(ctx context.Context, port int, dirs []string) (chan error, chan error, error) {
	sm := web.NewSessionManager()
	watcher, err := web.NewWatcher(dirs, sm)
	if err != nil {
		return nil, nil, fmt.Errorf("create watcher: %w", err)
	}

	serverCfg := web.ServerConfig{
		Port:     port,
		PlanName: "(watch mode)",
		Branch:   "",
		PlanFile: "",
	}

	srv, err := web.NewServerWithSessions(serverCfg, sm)
	if err != nil {
		return nil, nil, fmt.Errorf("create web server: %w", err)
	}

	// start server with startup check
	srvErrCh, err := startServerAsync(ctx, srv, port)
	if err != nil {
		return nil, nil, err
	}

	// start watcher in background
	watchErrCh := make(chan error, 1)
	go func() {
		if watchErr := watcher.Start(ctx); watchErr != nil {
			watchErrCh <- watchErr
		}
		close(watchErrCh)
	}()

	return srvErrCh, watchErrCh, nil
}

// printWatchModeInfo prints startup information for watch-only mode.
func printWatchModeInfo(dirs []string, port int) {
	fmt.Printf("watch-only mode: monitoring %d directories\n", len(dirs))
	for _, dir := range dirs {
		fmt.Printf("  %s\n", dir)
	}
	fmt.Printf("web dashboard: http://localhost:%d\n", port)
	fmt.Printf("press Ctrl+C to exit\n")
}

// serverStartupTimeout is the time to wait for server startup before assuming success.
const serverStartupTimeout = 100 * time.Millisecond

// startServerAsync starts a web server in the background and waits briefly for startup errors.
// returns the error channel for monitoring late errors, or an error if startup fails.
func startServerAsync(ctx context.Context, srv *web.Server, port int) (chan error, error) {
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(ctx); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	// wait briefly for startup errors
	select {
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("web server failed to start on port %d: %w", port, err)
		}
	case <-time.After(serverStartupTimeout):
		// server started successfully
	}

	return errCh, nil
}

// monitorWatchMode monitors server and watcher error channels until shutdown.
func monitorWatchMode(ctx context.Context, srvErrCh, watchErrCh chan error) error {
	for {
		// exit when both channels are nil (closed and handled)
		if srvErrCh == nil && watchErrCh == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case srvErr, ok := <-srvErrCh:
			if !ok {
				srvErrCh = nil
				continue
			}
			if srvErr != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "web server error: %v\n", srvErr)
			}
		case watchErr, ok := <-watchErrCh:
			if !ok {
				watchErrCh = nil
				continue
			}
			if watchErr != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "file watcher error: %v\n", watchErr)
			}
		}
	}
}

// findRecentPlan finds the most recently modified .md file in plansDir
// that was modified after startTime. Returns empty string if none found.
func findRecentPlan(plansDir string, startTime time.Time) string {
	// find all .md files in plansDir (excluding completed/ subdirectory)
	pattern := filepath.Join(plansDir, "*.md")
	plans, err := filepath.Glob(pattern)
	if err != nil || len(plans) == 0 {
		return ""
	}

	var recentPlan string
	var recentTime time.Time

	for _, plan := range plans {
		info, statErr := os.Stat(plan)
		if statErr != nil {
			continue
		}
		// file must be modified after startTime
		if info.ModTime().Before(startTime) {
			continue
		}
		// find the most recent one
		if recentPlan == "" || info.ModTime().After(recentTime) {
			recentPlan = plan
			recentTime = info.ModTime()
		}
	}

	return recentPlan
}

// startWebDashboard creates the web server and broadcast logger, starting the server in background.
// returns the broadcast logger to use for execution, or error if server fails to start.
// when watchDirs is non-empty, creates multi-session mode with file watching.
func startWebDashboard(ctx context.Context, p webDashboardParams) (processor.Logger, error) {
	// create session for SSE streaming (handles both live streaming and history replay)
	session := web.NewSession("main", p.BaseLog.Path())
	broadcastLog := web.NewBroadcastLogger(p.BaseLog, session)

	// extract plan name for display
	planName := "(no plan)"
	if p.PlanFile != "" {
		planName = filepath.Base(p.PlanFile)
	}

	cfg := web.ServerConfig{
		Port:     p.Port,
		PlanName: planName,
		Branch:   p.Branch,
		PlanFile: p.PlanFile,
	}

	// determine if we should use multi-session mode
	// multi-session mode is enabled when watch dirs are provided via CLI or config
	useMultiSession := len(p.WatchDirs) > 0 || len(p.ConfigWatchDirs) > 0

	var srv *web.Server
	var watcher *web.Watcher

	if useMultiSession {
		// multi-session mode: use SessionManager and Watcher
		sm := web.NewSessionManager()

		// register the live execution session so dashboard uses it instead of creating a duplicate
		// this ensures live events from BroadcastLogger go to the same session the dashboard displays
		sm.Register(session)

		// resolve watch directories (CLI > config > cwd)
		dirs := web.ResolveWatchDirs(p.WatchDirs, p.ConfigWatchDirs)

		var err error
		watcher, err = web.NewWatcher(dirs, sm)
		if err != nil {
			return nil, fmt.Errorf("create watcher: %w", err)
		}

		srv, err = web.NewServerWithSessions(cfg, sm)
		if err != nil {
			return nil, fmt.Errorf("create web server: %w", err)
		}
	} else {
		// single-session mode: direct session for current execution
		var err error
		srv, err = web.NewServer(cfg, session)
		if err != nil {
			return nil, fmt.Errorf("create web server: %w", err)
		}
	}

	// start server with startup check
	srvErrCh, err := startServerAsync(ctx, srv, p.Port)
	if err != nil {
		return nil, err
	}

	// start watcher in background if multi-session mode
	if watcher != nil {
		go func() {
			if watchErr := watcher.Start(ctx); watchErr != nil {
				// log error but don't fail - server can still work
				if p.Sender != nil {
					p.Sender.Send(tui.OutputMsg{Text: fmt.Sprintf("warning: watcher error: %v", watchErr)})
				}
			}
		}()
	}

	// monitor for late server errors in background
	// these are logged but don't fail the main execution since the dashboard is supplementary
	go func() {
		if srvErr := <-srvErrCh; srvErr != nil {
			if p.Sender != nil {
				p.Sender.Send(tui.OutputMsg{Text: fmt.Sprintf("warning: web server error during execution: %v", srvErr)})
			}
		}
	}()

	if p.Sender != nil {
		p.Sender.Send(tui.OutputMsg{Text: fmt.Sprintf("web dashboard: http://localhost:%d", p.Port)})
	}
	return broadcastLog, nil
}

// runReset runs the interactive config reset flow.
func runReset() error {
	configDir := config.DefaultConfigDir()
	_, err := config.Reset(configDir, os.Stdin, os.Stdout)
	if err != nil {
		return fmt.Errorf("reset config: %w", err)
	}
	return nil
}

// isResetOnly returns true if --reset was the only meaningful flag/arg specified.
// this allows reset to work standalone (exit after reset) while also supporting
// combined usage like "ralphex --reset docs/plans/feature.md".
func isResetOnly(o opts) bool {
	return o.PlanFile == "" && !o.Review && !o.CodexOnly && !o.Serve && o.PlanDescription == "" && len(o.Watch) == 0
}
