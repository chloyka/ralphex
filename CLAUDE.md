# ralphex

Autonomous plan execution with Claude Code - Go rewrite of ralph.py.

## LLM Documentation

See @llms.txt for usage instructions and Claude Code integration commands.

## Build Commands

```bash
make build      # build binary to .bin/ralphex
make test       # run tests with coverage
make lint       # run golangci-lint
make fmt        # format code
```

## Project Structure

```
cmd/ralphex/        # main entry point, CLI parsing
pkg/config/         # configuration loading, defaults, prompts, agents
pkg/executor/       # claude and codex CLI execution
pkg/git/            # git operations using go-git library
pkg/processor/      # orchestration loop, prompts, signals
pkg/progress/       # timestamped file logging, progress file management
pkg/tui/            # Bubble Tea terminal UI (views, logger, input collector)
pkg/web/            # web dashboard, SSE streaming, session management
docs/plans/         # plan files location
```

## Code Style

- Use jessevdk/go-flags for CLI parsing
- All comments lowercase except godoc
- Table-driven tests with testify
- 80%+ test coverage target

## Key Patterns

- Signal-based completion detection (COMPLETED, FAILED, REVIEW_DONE signals)
- Plan creation signals: QUESTION (with JSON payload) and PLAN_READY
- Streaming output with timestamps
- Progress logging to files
- Progress file locking (flock) for active session detection
- Multiple execution modes: full, review-only, codex-only, plan creation
- Configuration via `~/.config/ralphex/` with embedded defaults
- File watching for multi-session dashboard using fsnotify

### Plan Creation Mode

The `--plan "description"` flag enables interactive plan creation:

- Claude explores codebase and asks clarifying questions
- Questions use QUESTION signal with JSON: `{"question": "...", "options": [...]}`
- User answers via TUI option picker
- Q&A history stored in progress file for context
- Loop continues until PLAN_READY signal
- Plan file written to docs/plans/
- After completion, prompts user: "Continue with plan implementation?"
- If "Yes", creates branch and runs full execution mode on the new plan

Key files:
- `pkg/tui/input_collector.go` - TUI input collector (Bubble Tea)
- `pkg/tui/plan_create.go` - plan description textarea view
- `pkg/processor/signals.go` - QUESTION/PLAN_READY signal parsing
- `pkg/config/defaults/prompts/make_plan.txt` - plan creation prompt

### TUI Architecture (Bubble Tea)

All terminal I/O goes through the Bubble Tea TUI (`pkg/tui/`). The TUI provides:
- Plan selection with fuzzy filtering (replaces fzf subprocess)
- Plan creation via textarea widget
- Scrolling log viewport for streaming execution output
- Question/answer prompts during plan creation mode
- Status bar with phase, elapsed time, and plan name

Key files in `pkg/tui/`:
- `tui.go` - main Model with state machine (Init/Update/View), states: SelectPlan, CreatePlan, Executing, Question, Done
- `messages.go` - all custom tea.Msg types (OutputMsg, PhaseChangeMsg, QuestionMsg, PlanSelectedMsg, etc.)
- `styles.go` - lipgloss styles for colored terminal output
- `logger.go` - teaLogger implementing processor.Logger (writes to file + sends tea.Msg); also defines `Sender` interface and `SafeSender` wrapper
- `input_collector.go` - teaCollector implementing processor.InputCollector (question/answer and yes/no prompts via TUI)
- `viewport.go` - viewport configuration, header/status bar rendering
- `plan_list.go` - plan file list with bubbles/list component
- `plan_create.go` - plan description textarea with bubbles/textarea component

**TUI/business-logic coordination:**
- `Sender` interface (`logger.go`) - abstraction for sending `tea.Msg` to the TUI program
- `SafeSender` (`logger.go`) - wraps `Sender`, becomes a no-op after `Stop()` is called; prevents `p.Send()` from blocking after TUI exits (Ctrl+C). Created in `run()` (main.go) and passed to `runBusinessLogic`.
- `runBusinessLogic` always sends `ExecutionDoneMsg` when done, using `SafeSender` so it's safe even after TUI quit
- `teaCollector.AskYesNo()` (`input_collector.go`) - safe yes/no prompts with context cancellation; used instead of raw channel bridging

Dependencies: `charmbracelet/bubbletea`, `charmbracelet/lipgloss`, `charmbracelet/bubbles`

## Platform Support

- **Linux/macOS:** fully supported
- **Windows:** builds and runs, but with limitations:
  - Process group signals not available (graceful shutdown kills direct process only, not child processes)
  - File locking not available (active session detection disabled)

### Cross-Platform Development

When adding platform-specific code (syscalls, signals, file locking):
1. Use build tags: `//go:build !windows` for Unix-only code, `//go:build windows` for Windows stubs
2. Create separate files: `foo_unix.go` and `foo_windows.go`
3. Keep common code in the main file, extract platform-specific functions
4. Windows stubs can be no-ops where functionality is optional

Example files:
- `pkg/executor/procgroup_unix.go` / `procgroup_windows.go` - process group management
- `pkg/progress/flock_unix.go` / `flock_windows.go` - file locking helpers

Cross-compile to verify Windows builds:
```bash
GOOS=windows GOARCH=amd64 go build ./...
```

## Configuration

- Global config location: `~/.config/ralphex/`
- Local config location: `.ralphex/` (per-project, optional)
- Config file format: INI (using gopkg.in/ini.v1)
- Embedded defaults in `pkg/config/defaults/`
- Precedence: CLI flags > local config > global config > embedded defaults
- Custom prompts: `~/.config/ralphex/prompts/*.txt` or `.ralphex/prompts/*.txt`
- Custom agents: `~/.config/ralphex/agents/*.txt` or `.ralphex/agents/*.txt`

### Local Project Config (.ralphex/)

Projects can have local configuration that overrides global settings:

```
project/
├── .ralphex/           # optional, project-local config
│   ├── config          # overrides specific settings (per-field merge)
│   ├── prompts/        # per-file fallback: local → global → embedded
│   │   └── task.txt    # only override task prompt
│   └── agents/         # replaces global if has files (no merge)
│       └── custom.txt  # project-specific agent
```

**Merge strategy:**
- **Config file**: per-field override (local values override global, missing fields fall back)
- **Prompts**: per-file fallback (local → global → embedded for each prompt file)
- **Agents**: replace entirely (if local agents/ has .txt files, use ONLY local agents)

### Config Defaults Behavior

- **config file**: copied on first run, always exists
- **scalars/colors**: per-field fallback to embedded defaults if missing
- **prompts**: copied if dir empty, per-file fallback to embedded if deleted
- **agents**: copied if dir empty, no fallback (user controls full set)
- `*Set` flags (e.g., `CodexEnabledSet`) distinguish explicit `false`/`0` from "not set"
- If ANY `.txt` exists in prompts/ or agents/, no defaults copied (user manages that dir)

### Agent System

5 default agents are installed on first run to `~/.config/ralphex/agents/`:
- `implementation.txt` - verifies code achieves stated goals
- `quality.txt` - reviews for bugs, security issues, race conditions
- `documentation.txt` - checks if docs need updates
- `simplification.txt` - detects over-engineering
- `testing.txt` - reviews test coverage and quality

**Template syntax:** Use `{{agent:name}}` in prompt files to reference agents. Each reference expands to Task tool instructions that tell Claude Code to run that agent.

**Customization:**
- Edit files in `~/.config/ralphex/agents/` to modify agent prompts
- Add new `.txt` files to create custom agents
- Run `ralphex --reset` to interactively restore defaults, or delete ALL `.txt` files manually
- Alternatively, reference agents installed in your Claude Code directly in prompt files (like `qa-expert`, `go-smells-expert`)

## Testing

```bash
go test ./...           # run all tests
go test -cover ./...    # with coverage
```

## End-to-End Testing

Unit tests mock external calls. After ANY code changes, run e2e test with a toy project to verify actual claude/codex integration and output streaming.

### Create Toy Project

```bash
./scripts/prep-toy-test.sh
```

This creates `/tmp/ralphex-test` with a buggy Go file and a plan to fix it.

### Test Full Mode

```bash
cd /tmp/ralphex-test
.bin/ralphex docs/plans/fix-issues.md
```

**Expected behavior:**
1. Creates branch `fix-issues`
2. Phase 1: executes Task 1, then Task 2
3. Phase 2: first Claude review
4. Phase 2.5: codex external review
5. Phase 3: second Claude review
6. Moves plan to `docs/plans/completed/`

### Test Review-Only Mode

```bash
cd /tmp/ralphex-test
git checkout -b feature-test

# make some changes
echo "// comment" >> main.go
git add -A && git commit -m "add comment"

# run review-only (no plan needed)
go run <ralphex-project-root>/cmd/ralphex --review
```

### Test Codex-Only Mode

```bash
cd /tmp/ralphex-test

# run codex-only review
go run <ralphex-project-root>/cmd/ralphex --codex-only
```

### Monitor Progress

```bash
# live stream (use actual filename from ralphex output)
tail -f progress-fix-issues.txt

# recent activity
tail -50 progress-*.txt
```

## Development Workflow

**CRITICAL: After ANY code changes to ralphex:**

1. Run unit tests: `make test`
2. Run linter: `make lint`
3. **MUST** run end-to-end test with toy project (see above)
4. Monitor `tail -f progress-*.txt` to verify output streaming works

Unit tests don't verify actual codex/claude integration or output formatting. The toy project test is the only way to verify streaming output works correctly.

## Before Submitting a PR

If you're an AI agent preparing a contribution, complete this checklist:

**Code Quality:**
- [ ] Run `make test` - all tests must pass
- [ ] Run `make lint` - fix all linter issues
- [ ] Run `make fmt` - code is properly formatted
- [ ] New code has tests with 80%+ coverage

**Project Patterns:**
- [ ] Studied existing code to understand project conventions
- [ ] One `_test.go` file per source file (not `foo_something_test.go`)
- [ ] Tests use table-driven pattern with testify
- [ ] Test helper functions call `t.Helper()`
- [ ] Mocks generated with moq, stored in `mocks/` subdirectory
- [ ] Interfaces defined at consumer side, not provider
- [ ] Context as first parameter for blocking/cancellable methods
- [ ] Private struct fields for internal state, accessor methods if needed
- [ ] Regex patterns compiled once at package level
- [ ] Deferred cleanup for resources (files, contexts, connections)
- [ ] No new dependencies unless directly needed - avoid accidental additions

**PR Scope:**
- [ ] Changes are focused on the requested feature/fix only
- [ ] No "general improvements" to unrelated code
- [ ] PR is reasonably sized for human review
- [ ] Large changes split into logical, focused PRs

**Self-Review:**
- [ ] Can explain every line of code if asked
- [ ] Checked for security issues (injection, secrets exposure, etc.)
- [ ] Commit messages describe "why", not just "what"

## MkDocs Site

- Site source: `site/` directory with `mkdocs.yml`
- Template overrides: `site/overrides/` with `custom_dir: overrides` in mkdocs.yml
- **CI constraint**: Cloudflare Pages uses mkdocs-material 9.2.x, must use `materialx.emoji` syntax (not `material.extensions.emoji` which requires 9.4+)
- **Raw .md files**: MkDocs renders ALL `.md` files in `docs_dir` as HTML pages. To serve raw markdown (e.g., `assets/claude/*.md` for Claude Code skills), copy them AFTER `mkdocs build` - see `prep_site` target in Makefile

## Workflow Rules

- **CHANGELOG**: Never modify during development - updates are part of release process only
- **Version sections**: Never add entries to existing version sections - versions are immutable once released
- **Linter warnings**: Add exclusions to `.golangci.yml` instead of `_, _ =` prefixes for fmt.Fprintf/Fprintln
- **Exporting functions**: When changing visibility (lowercase to uppercase), check ALL callers including test files
