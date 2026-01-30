package progress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/processor"
)

// testColors returns a Colors stub for testing.
func testColors() *Colors {
	return NewColors(config.ColorConfig{
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

func TestNewLogger(t *testing.T) {
	tmpDir := t.TempDir()
	colors := testColors()

	tests := []struct {
		name     string
		cfg      Config
		wantPath string
	}{
		{name: "full mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "full", Branch: "main"}, wantPath: "progress-feature.txt"},
		{name: "review mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "review", Branch: "main"}, wantPath: "progress-feature-review.txt"},
		{name: "codex-only mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "codex-only", Branch: "main"}, wantPath: "progress-feature-codex.txt"},
		{name: "full mode no plan", cfg: Config{Mode: "full", Branch: "main"}, wantPath: "progress.txt"},
		{name: "review mode no plan", cfg: Config{Mode: "review", Branch: "main"}, wantPath: "progress-review.txt"},
		{name: "codex-only mode no plan", cfg: Config{Mode: "codex-only", Branch: "main"}, wantPath: "progress-codex.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// change to tmpDir for test
			origDir, _ := os.Getwd()
			require.NoError(t, os.Chdir(tmpDir))
			defer func() { _ = os.Chdir(origDir) }()

			l, err := NewLogger(tc.cfg, colors)
			require.NoError(t, err)
			defer l.Close()

			assert.Equal(t, tc.wantPath, filepath.Base(l.Path()))

			// verify header written
			content, err := os.ReadFile(l.Path())
			require.NoError(t, err)
			assert.Contains(t, string(content), "# Ralphex Progress Log")
			assert.Contains(t, string(content), "Mode: "+tc.cfg.Mode)
		})
	}
}

func TestLogger_Print(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.Print("test message %d", 42)

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "test message 42")
}

func TestLogger_PrintRaw(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.PrintRaw("raw output")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "raw output")
}

func TestLogger_PrintSection(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	section := processor.NewGenericSection("test section")
	l.PrintSection(section)

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "--- test section ---")
}

func TestLogger_PrintAligned(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.PrintAligned("first line\nsecond line\nthird line")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	// check file has timestamps and proper formatting
	assert.Contains(t, string(content), "] first line")
	assert.Contains(t, string(content), "second line")
	assert.Contains(t, string(content), "third line")
}

func TestLogger_PrintAligned_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	// get file size before
	info, err := os.Stat(l.Path())
	require.NoError(t, err)
	sizeBefore := info.Size()

	l.PrintAligned("") // empty string should do nothing

	info, err = os.Stat(l.Path())
	require.NoError(t, err)
	assert.Equal(t, sizeBefore, info.Size(), "file should not grow for empty PrintAligned")
}

func TestLogger_Error(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.Error("something failed: %s", "reason")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "ERROR: something failed: reason")
}

func TestLogger_Warn(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.Warn("warning message")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "WARN: warning message")
}

func TestLogger_SetPhase(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.SetPhase(processor.PhaseTask)
	l.Print("task output")

	l.SetPhase(processor.PhaseReview)
	l.Print("review output")

	l.SetPhase(processor.PhaseCodex)
	l.Print("codex output")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "task output")
	assert.Contains(t, contentStr, "review output")
	assert.Contains(t, contentStr, "codex output")
}

func TestLogger_Elapsed(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)
	defer l.Close()

	elapsed := l.Elapsed()
	// go-humanize returns "now" for very short durations
	assert.NotEmpty(t, elapsed)
}

func TestLogger_Close(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors())
	require.NoError(t, err)

	l.Print("some output")
	err = l.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "Completed:")
	assert.Contains(t, string(content), strings.Repeat("-", 60))
}

func TestGetProgressFilename(t *testing.T) {
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
		{"full with date prefix", "plans/2024-01-15-refactor.md", "", "full", "progress-2024-01-15-refactor.txt"},
		{"plan mode with description", "", "implement caching", "plan", "progress-plan-implement-caching.txt"},
		{"plan mode with complex description", "", "Add User Authentication!", "plan", "progress-plan-add-user-authentication.txt"},
		{"plan mode no description", "", "", "plan", "progress-plan.txt"},
		{"plan mode with special chars", "", "fix: bug #123", "plan", "progress-plan-fix-bug-123.txt"},
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
		{"multiple spaces", "add   feature", "add-feature"},
		{"leading trailing dashes", "--test--", "test"},
		{"only special chars", "!@#$%", "unnamed"},
		{"empty string", "", "unnamed"},
		{"long description", strings.Repeat("a", 60), strings.Repeat("a", 50)},
		{"long with spaces", "this is a very long plan description that exceeds the maximum length", "this-is-a-very-long-plan-description-that-exceeds"},
		{"numbers", "feature 123", "feature-123"},
		{"mixed", "API v2.0 endpoint", "api-v20-endpoint"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizePlanName(tc.input)
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
		{"12. item twelve", true},
		{"123. large number", true},
		{"- bullet item", true},
		{"* star item", true},
		{"regular text", false},
		{"1 no dot", false},
		{"1.no space", false},
		{".1 dot first", false},
		{"", false},
		{"  - already indented", false}, // has leading space, won't match
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := isListItem(tc.input)
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
		{"star list", "* star item", "  * star item"},
		{"regular text", "regular text", "regular text"},
		{"already indented", "  - item", "  - item"},
		{"double digit", "12. item", "  12. item"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatListItem(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNewColors(t *testing.T) {
	t.Run("creates colors from valid config", func(t *testing.T) {
		cfg := config.ColorConfig{
			Task:       "0,255,0",
			Review:     "0,255,255",
			Codex:      "255,0,255",
			ClaudeEval: "100,200,255",
			Warn:       "255,255,0",
			Error:      "255,0,0",
			Signal:     "255,100,100",
			Timestamp:  "138,138,138",
			Info:       "180,180,180",
		}
		colors := NewColors(cfg)
		assert.NotNil(t, colors)
	})

	t.Run("panics on invalid task color", func(t *testing.T) {
		cfg := config.ColorConfig{
			Task:       "invalid",
			Review:     "0,255,255",
			Codex:      "255,0,255",
			ClaudeEval: "100,200,255",
			Warn:       "255,255,0",
			Error:      "255,0,0",
			Signal:     "255,100,100",
			Timestamp:  "138,138,138",
			Info:       "180,180,180",
		}
		assert.Panics(t, func() { NewColors(cfg) })
	})

	t.Run("panics on empty color", func(t *testing.T) {
		cfg := config.ColorConfig{
			Task:       "",
			Review:     "0,255,255",
			Codex:      "255,0,255",
			ClaudeEval: "100,200,255",
			Warn:       "255,255,0",
			Error:      "255,0,0",
			Signal:     "255,100,100",
			Timestamp:  "138,138,138",
			Info:       "180,180,180",
		}
		assert.Panics(t, func() { NewColors(cfg) })
	})
}

func TestValidateColorOrPanic(t *testing.T) {
	t.Run("valid colors", func(t *testing.T) {
		tests := []struct {
			name string
			s    string
		}{
			{name: "red", s: "255,0,0"},
			{name: "black", s: "0,0,0"},
			{name: "white", s: "255,255,255"},
			{name: "with spaces", s: " 100 , 150 , 200 "},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				assert.NotPanics(t, func() {
					validateColorOrPanic(tc.s, "test")
				})
			})
		}
	})

	t.Run("invalid colors panic", func(t *testing.T) {
		tests := []struct {
			name string
			s    string
		}{
			{name: "empty string", s: ""},
			{name: "too few parts", s: "255,0"},
			{name: "too many parts", s: "255,0,0,0"},
			{name: "invalid r component", s: "abc,0,0"},
			{name: "invalid g component", s: "0,abc,0"},
			{name: "invalid b component", s: "0,0,abc"},
			{name: "r out of range high", s: "256,0,0"},
			{name: "g out of range high", s: "0,256,0"},
			{name: "b out of range high", s: "0,0,256"},
			{name: "r out of range negative", s: "-1,0,0"},
			{name: "g out of range negative", s: "0,-1,0"},
			{name: "b out of range negative", s: "0,0,-1"},
			{name: "single value", s: "255"},
			{name: "no delimiter", s: "255000"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				assert.Panics(t, func() {
					validateColorOrPanic(tc.s, "test")
				})
			})
		}
	})
}

func TestLogger_LogQuestion(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "plan", PlanDescription: "test", Branch: "main"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.LogQuestion("Which cache backend?", []string{"Redis", "In-memory", "File-based"})

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "QUESTION: Which cache backend?")
	assert.Contains(t, contentStr, "OPTIONS: Redis, In-memory, File-based")
}

func TestLogger_LogAnswer(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "plan", PlanDescription: "test", Branch: "main"}, testColors())
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.LogAnswer("Redis")

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "ANSWER: Redis")
}

func TestLogger_PlanModeFilename(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	tests := []struct {
		name        string
		cfg         Config
		wantPath    string
		wantContent string
	}{
		{
			name:        "plan mode with description",
			cfg:         Config{Mode: "plan", PlanDescription: "implement caching", Branch: "main"},
			wantPath:    "progress-plan-implement-caching.txt",
			wantContent: "Mode: plan",
		},
		{
			name:        "plan mode without description",
			cfg:         Config{Mode: "plan", Branch: "main"},
			wantPath:    "progress-plan.txt",
			wantContent: "Mode: plan",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := NewLogger(tc.cfg, testColors())
			require.NoError(t, err)
			defer l.Close()

			assert.Equal(t, tc.wantPath, filepath.Base(l.Path()))

			content, err := os.ReadFile(l.Path())
			require.NoError(t, err)
			assert.Contains(t, string(content), tc.wantContent)
		})
	}
}
