package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
)

// Sender sends messages to a Bubble Tea program. Implemented by *tea.Program.
type Sender interface {
	Send(msg tea.Msg)
}

// SafeSender wraps a Sender and becomes a no-op after Stop is called.
// this prevents blocking on p.Send() after the TUI event loop has exited.
// uses a mutex to eliminate the TOCTOU race between checking stopped and calling Send.
type SafeSender struct {
	sender  Sender
	mu      sync.RWMutex
	stopped bool
}

// NewSafeSender creates a SafeSender wrapping the given Sender.
func NewSafeSender(sender Sender) *SafeSender {
	return &SafeSender{sender: sender}
}

// Send sends a message to the wrapped Sender, unless Stop has been called.
// holds a read lock during the send, so Stop blocks until all in-flight sends complete.
func (s *SafeSender) Send(msg tea.Msg) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.stopped {
		return
	}
	s.sender.Send(msg)
}

// Stop marks the sender as stopped. All subsequent Send calls become no-ops.
// safe to call multiple times.
func (s *SafeSender) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
}

// LoggerConfig holds configuration for creating a TUI logger.
type LoggerConfig struct {
	PlanFile        string // plan filename (used to derive progress filename)
	PlanDescription string // plan description for plan mode (used for filename)
	Mode            string // execution mode: full, review, codex-only, plan
	Branch          string // current git branch
}

// teaLogger implements processor.Logger by writing to a progress file
// and sending tea.Msg to the Bubble Tea program for TUI display.
type teaLogger struct {
	file      *os.File
	sender    Sender
	startTime time.Time
	phase     processor.Phase
}

// timestampFormat is the format for timestamps: YY-MM-DD HH:MM:SS
const timestampFormat = "06-01-02 15:04:05"

// NewLogger creates a TUI logger that writes to a progress file and sends
// messages to the Bubble Tea program. The progress file is locked exclusively
// to signal an active session.
func NewLogger(cfg LoggerConfig, sender Sender) (*teaLogger, error) {
	progressPath := progressFilename(cfg.PlanFile, cfg.PlanDescription, cfg.Mode)

	// ensure progress files dir exists
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
	if err := progress.LockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire file lock: %w", err)
	}
	progress.RegisterActiveLock(f.Name())

	l := &teaLogger{
		file:      f,
		sender:    sender,
		startTime: time.Now(),
		phase:     processor.PhaseTask,
	}

	// write header to file
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
func (l *teaLogger) Path() string {
	if l.file == nil {
		return ""
	}
	return l.file.Name()
}

// SetPhase sets the current execution phase for color coding.
func (l *teaLogger) SetPhase(phase processor.Phase) {
	l.phase = phase
	l.sender.Send(PhaseChangeMsg{Phase: phase})
}

// Print writes a timestamped message to the progress file and sends an OutputMsg.
func (l *teaLogger) Print(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] %s\n", timestamp, msg)
	l.sender.Send(OutputMsg{Text: fmt.Sprintf("[%s] %s", timestamp, msg)})
}

// PrintRaw writes without timestamp to the progress file and sends an OutputMsg.
func (l *teaLogger) PrintRaw(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.writeFile("%s", msg)
	l.sender.Send(OutputMsg{Text: msg})
}

// PrintSection writes a section header to the progress file and sends a SectionMsg.
func (l *teaLogger) PrintSection(section processor.Section) {
	header := fmt.Sprintf("\n--- %s ---\n", section.Label)
	l.writeFile("%s", header)
	l.sender.Send(SectionMsg{Section: section})
}

// PrintAligned writes text with timestamp on each line to the progress file
// and sends an OutputMsg with the text content.
func (l *teaLogger) PrintAligned(text string) {
	if text == "" {
		return
	}

	// trim trailing newlines
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}

	lines := strings.Split(text, "\n")
	var outputLines []string

	for _, line := range lines {
		if line == "" {
			continue
		}

		displayLine := formatListItem(line)
		timestamp := time.Now().Format(timestampFormat)
		l.writeFile("[%s] %s\n", timestamp, displayLine)
		outputLines = append(outputLines, fmt.Sprintf("[%s] %s", timestamp, displayLine))
	}

	if len(outputLines) > 0 {
		l.sender.Send(OutputMsg{Text: strings.Join(outputLines, "\n")})
	}
}

// LogQuestion logs a question and its options to the progress file
// and sends a QuestionMsg for display.
func (l *teaLogger) LogQuestion(question string, options []string) {
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] QUESTION: %s\n", timestamp, question)
	l.writeFile("[%s] OPTIONS: %s\n", timestamp, strings.Join(options, ", "))

	l.sender.Send(OutputMsg{Text: fmt.Sprintf("[%s] QUESTION: %s", timestamp, question)})
	l.sender.Send(OutputMsg{Text: fmt.Sprintf("[%s] OPTIONS: %s", timestamp, strings.Join(options, ", "))})
}

// LogAnswer logs the user's answer to the progress file and sends an OutputMsg.
func (l *teaLogger) LogAnswer(answer string) {
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] ANSWER: %s\n", timestamp, answer)
	l.sender.Send(OutputMsg{Text: fmt.Sprintf("[%s] ANSWER: %s", timestamp, answer)})
}

// Elapsed returns formatted elapsed time since the logger was created.
func (l *teaLogger) Elapsed() string {
	d := time.Since(l.startTime)
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// Close writes footer, releases the file lock, and closes the progress file.
func (l *teaLogger) Close() error {
	if l.file == nil {
		return nil
	}

	l.writeFile("\n%s\n", strings.Repeat("-", 60))
	l.writeFile("Completed: %s\n", time.Now().Format("2006-01-02 15:04:05"))

	// release file lock before closing
	_ = progress.UnlockFile(l.file)
	progress.UnregisterActiveLock(l.file.Name())

	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close progress file: %w", err)
	}
	return nil
}

func (l *teaLogger) writeFile(format string, args ...any) {
	if l.file != nil {
		fmt.Fprintf(l.file, format, args...)
	}
}

// formatListItem adds 2-space indent for list items (numbered or bulleted).
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
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return true
	}
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

// progressFilename returns the progress file path based on plan and mode.
// mirrors the logic in pkg/progress/progress.go.
func progressFilename(planFile, planDescription, mode string) string {
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
func sanitizePlanName(desc string) string {
	result := strings.ToLower(desc)
	result = strings.ReplaceAll(result, " ", "-")

	var clean strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			clean.WriteRune(r)
		}
	}
	result = clean.String()

	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	result = strings.Trim(result, "-")

	if len(result) > 50 {
		result = result[:50]
		result = strings.TrimRight(result, "-")
	}

	if result == "" {
		return "unnamed"
	}
	return result
}
