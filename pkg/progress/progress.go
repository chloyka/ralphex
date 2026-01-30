// Package progress provides timestamped logging to a progress file.
package progress

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/processor"
)

// Logger writes timestamped output to a progress file.
type Logger struct {
	file      *os.File
	startTime time.Time
	phase     processor.Phase
}

// Config holds logger configuration.
type Config struct {
	PlanFile        string // plan filename (used to derive progress filename)
	PlanDescription string // plan description for plan mode (used for filename)
	Mode            string // execution mode: full, review, codex-only, plan
	Branch          string // current git branch
}

// NewColors is kept for backward compatibility. It validates color config strings
// but returns nil since colors are now handled by the TUI package's lipgloss styles.
func NewColors(cfg config.ColorConfig) *Colors {
	// validate all color values to preserve the panic-on-invalid-config behavior
	validateColorOrPanic(cfg.Task, "task")
	validateColorOrPanic(cfg.Review, "review")
	validateColorOrPanic(cfg.Codex, "codex")
	validateColorOrPanic(cfg.ClaudeEval, "claude_eval")
	validateColorOrPanic(cfg.Warn, "warn")
	validateColorOrPanic(cfg.Error, "error")
	validateColorOrPanic(cfg.Signal, "signal")
	validateColorOrPanic(cfg.Timestamp, "timestamp")
	validateColorOrPanic(cfg.Info, "info")
	return &Colors{}
}

// Colors is a deprecated stub. Color rendering is now handled by the TUI package (lipgloss).
// This type exists for backward compatibility with code that passes *Colors around.
type Colors struct{}

// validateColorOrPanic validates an RGB color string, panicking if invalid.
func validateColorOrPanic(s, name string) {
	if s == "" {
		panic(fmt.Sprintf("invalid color_%s value: %q", name, s))
	}
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		panic(fmt.Sprintf("invalid color_%s value: %q", name, s))
	}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		var v int
		if _, err := fmt.Sscanf(trimmed, "%d", &v); err != nil || v < 0 || v > 255 {
			panic(fmt.Sprintf("invalid color_%s value: %q", name, s))
		}
	}
}

// NewLogger creates a logger writing to a progress file.
func NewLogger(cfg Config, colors *Colors) (*Logger, error) {
	progressPath := progressFilename(cfg.PlanFile, cfg.PlanDescription, cfg.Mode)

	// ensure progress files are tracked by creating parent dir
	if dir := filepath.Dir(progressPath); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create progress dir: %w", err)
		}
	}

	f, err := os.Create(progressPath) //nolint:gosec // path derived from plan filename
	if err != nil {
		return nil, fmt.Errorf("create progress file: %w", err)
	}

	// acquire exclusive lock on progress file to signal active session
	// the lock is held for the duration of execution and released on Close()
	if err := LockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire file lock: %w", err)
	}
	RegisterActiveLock(f.Name())

	l := &Logger{
		file:      f,
		startTime: time.Now(),
		phase:     processor.PhaseTask,
	}

	// write header
	planStr := cfg.PlanFile
	if planStr == "" {
		planStr = "(no plan - review only)"
	}
	l.writeFile("# Ralphex Progress Log\n")
	l.writeFile("Plan: %s\n", planStr)
	l.writeFile("Branch: %s\n", cfg.Branch)
	l.writeFile("Mode: %s\n", cfg.Mode)
	l.writeFile("Started: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	l.writeFile("%s\n\n", strings.Repeat("-", 60))

	return l, nil
}

// Path returns the progress file path.
func (l *Logger) Path() string {
	if l.file == nil {
		return ""
	}
	return l.file.Name()
}

// SetPhase sets the current execution phase.
func (l *Logger) SetPhase(phase processor.Phase) {
	l.phase = phase
}

// timestampFormat is the format for timestamps: YY-MM-DD HH:MM:SS
const timestampFormat = "06-01-02 15:04:05"

// Print writes a timestamped message to the progress file.
func (l *Logger) Print(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)
	l.writeFile("[%s] %s\n", timestamp, msg)
}

// PrintRaw writes without timestamp (for streaming output).
func (l *Logger) PrintRaw(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.writeFile("%s", msg)
}

// PrintSection writes a section header without timestamp.
// format: "\n--- {label} ---\n"
func (l *Logger) PrintSection(section processor.Section) {
	header := fmt.Sprintf("\n--- %s ---\n", section.Label)
	l.writeFile("%s", header)
}

// PrintAligned writes text with timestamp on each line, suppressing empty lines.
func (l *Logger) PrintAligned(text string) {
	if text == "" {
		return
	}

	// trim trailing newlines to avoid extra blank lines
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}

	for line := range strings.SplitSeq(text, "\n") {
		if line == "" {
			continue // skip empty lines
		}

		// add indent for list items
		displayLine := formatListItem(line)

		// timestamp each line
		timestamp := time.Now().Format(timestampFormat)
		l.writeFile("[%s] %s\n", timestamp, displayLine)
	}
}

// formatListItem adds 2-space indent for list items (numbered or bulleted).
// detects patterns like "1. ", "12. ", "- ", "* " at line start.
func formatListItem(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == line { // no leading whitespace
		if isListItem(trimmed) {
			return "  " + line
		}
	}
	return line
}

// isListItem returns true if line starts with a list marker.
func isListItem(line string) bool {
	// check for "- " or "* " (bullet lists)
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return true
	}
	// check for numbered lists like "1. ", "12. ", "123. "
	for i, r := range line {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && i > 0 && i < len(line)-1 && line[i+1] == ' ' {
			return true
		}
		break
	}
	return false
}

// Error writes an error message to the progress file.
func (l *Logger) Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)
	l.writeFile("[%s] ERROR: %s\n", timestamp, msg)
}

// Warn writes a warning message to the progress file.
func (l *Logger) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)
	l.writeFile("[%s] WARN: %s\n", timestamp, msg)
}

// LogQuestion logs a question and its options for plan creation mode.
func (l *Logger) LogQuestion(question string, options []string) {
	timestamp := time.Now().Format(timestampFormat)
	l.writeFile("[%s] QUESTION: %s\n", timestamp, question)
	l.writeFile("[%s] OPTIONS: %s\n", timestamp, strings.Join(options, ", "))
}

// LogAnswer logs the user's answer for plan creation mode.
func (l *Logger) LogAnswer(answer string) {
	timestamp := time.Now().Format(timestampFormat)
	l.writeFile("[%s] ANSWER: %s\n", timestamp, answer)
}

// Elapsed returns formatted elapsed time since start.
func (l *Logger) Elapsed() string {
	return humanize.RelTime(l.startTime, time.Now(), "", "")
}

// Close writes footer, releases the file lock, and closes the progress file.
func (l *Logger) Close() error {
	if l.file == nil {
		return nil
	}

	l.writeFile("\n%s\n", strings.Repeat("-", 60))
	l.writeFile("Completed: %s (%s)\n", time.Now().Format("2006-01-02 15:04:05"), l.Elapsed())

	// release file lock before closing
	_ = UnlockFile(l.file)
	UnregisterActiveLock(l.file.Name())

	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close progress file: %w", err)
	}
	return nil
}

func (l *Logger) writeFile(format string, args ...any) {
	if l.file != nil {
		fmt.Fprintf(l.file, format, args...)
	}
}

// getProgressFilename returns progress file path based on plan and mode.
func progressFilename(planFile, planDescription, mode string) string {
	// plan mode uses sanitized plan description
	if mode == "plan" && planDescription != "" {
		sanitized := sanitizePlanName(planDescription)
		return fmt.Sprintf("progress-plan-%s.txt", sanitized)
	}

	if planFile != "" {
		stem := strings.TrimSuffix(filepath.Base(planFile), ".md")
		switch mode {
		case "codex-only":
			return fmt.Sprintf("progress-%s-codex.txt", stem)
		case "review":
			return fmt.Sprintf("progress-%s-review.txt", stem)
		default:
			return fmt.Sprintf("progress-%s.txt", stem)
		}
	}

	switch mode {
	case "codex-only":
		return "progress-codex.txt"
	case "review":
		return "progress-review.txt"
	case "plan":
		return "progress-plan.txt"
	default:
		return "progress.txt"
	}
}

// sanitizePlanName converts plan description to a safe filename component.
// replaces spaces with dashes, removes special characters, and limits length.
func sanitizePlanName(desc string) string {
	// lowercase and replace spaces with dashes
	result := strings.ToLower(desc)
	result = strings.ReplaceAll(result, " ", "-")

	// keep only alphanumeric and dashes
	var clean strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			clean.WriteRune(r)
		}
	}
	result = clean.String()

	// collapse multiple dashes
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	// trim leading/trailing dashes
	result = strings.Trim(result, "-")

	// limit length to 50 characters
	if len(result) > 50 {
		result = result[:50]
		// don't end with a dash
		result = strings.TrimRight(result, "-")
	}

	if result == "" {
		return "unnamed"
	}
	return result
}
