package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanItem_Interface(t *testing.T) {
	item := planItem{
		path: "/tmp/plans/feature.md",
		name: "feature",
		desc: "Add new feature",
	}

	assert.Equal(t, "feature", item.FilterValue())
	assert.Equal(t, "feature", item.Title())
	assert.Equal(t, "Add new feature", item.Description())
	assert.Equal(t, "/tmp/plans/feature.md", item.path)
}

func TestLoadPlanItems(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string // filename -> content
		wantCount int
		wantErr   bool
	}{
		{
			name: "multiple plans",
			files: map[string]string{
				"feature-a.md": "# Feature A\n\nDescription of feature A",
				"feature-b.md": "# Feature B\n\nDescription of feature B",
				"bugfix.md":    "Fix the login bug",
			},
			wantCount: 3,
		},
		{
			name:      "empty directory",
			files:     map[string]string{},
			wantCount: 0,
		},
		{
			name: "single plan",
			files: map[string]string{
				"only-plan.md": "# Only Plan",
			},
			wantCount: 1,
		},
		{
			name: "non-md files ignored",
			files: map[string]string{
				"plan.md":   "# Plan",
				"notes.txt": "some notes",
				"data.json": "{}",
			},
			wantCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			for name, content := range tc.files {
				err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
				require.NoError(t, err)
			}

			items, err := loadPlanItems(dir)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, items, tc.wantCount)

			// verify all items implement list.DefaultItem
			for _, item := range items {
				di, ok := item.(list.DefaultItem)
				require.True(t, ok, "item should implement list.DefaultItem")
				assert.NotEmpty(t, di.Title())
			}
		})
	}
}

func TestLoadPlanItems_Content(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "my-feature.md"), []byte("# My Feature\n\nDetailed description"), 0o600)
	require.NoError(t, err)

	items, err := loadPlanItems(dir)
	require.NoError(t, err)
	require.Len(t, items, 1)

	pi, ok := items[0].(planItem)
	require.True(t, ok)
	assert.Equal(t, "my-feature", pi.name)
	assert.Equal(t, "My Feature", pi.desc)
	assert.Equal(t, filepath.Join(dir, "my-feature.md"), pi.path)
}

func TestReadFirstLine(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "heading line",
			content:  "# My Plan\n\nSome details",
			expected: "My Plan",
		},
		{
			name:     "plain text",
			content:  "Simple description\nMore text",
			expected: "Simple description",
		},
		{
			name:     "empty lines before content",
			content:  "\n\n\nActual content",
			expected: "Actual content",
		},
		{
			name:     "frontmatter block skipped",
			content:  "---\ntitle: Plan\n---\n# Real Title",
			expected: "Real Title",
		},
		{
			name:     "empty file",
			content:  "",
			expected: "",
		},
		{
			name:     "only whitespace",
			content:  "   \n   \n   ",
			expected: "",
		},
		{
			name:     "h2 heading",
			content:  "## Sub Heading",
			expected: "Sub Heading",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "test.md")
			err := os.WriteFile(path, []byte(tc.content), 0o600)
			require.NoError(t, err)

			result := readFirstLine(path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestReadFirstLine_NonexistentFile(t *testing.T) {
	result := readFirstLine("/nonexistent/file.md")
	assert.Empty(t, result)
}

func TestNewPlanList(t *testing.T) {
	items := []list.Item{
		planItem{path: "/a.md", name: "plan-a", desc: "Plan A"},
		planItem{path: "/b.md", name: "plan-b", desc: "Plan B"},
	}

	l := newPlanList(items, 80, 24)
	assert.Equal(t, "select plan", l.Title)
	assert.Len(t, l.Items(), 2)
	assert.True(t, l.FilteringEnabled())
}

func TestNewPlanList_Empty(t *testing.T) {
	l := newPlanList(nil, 80, 24)
	assert.Empty(t, l.Items())
}

func TestLoadPlansCmd(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# Plan"), 0o600)
	require.NoError(t, err)

	cmd := loadPlansCmd(dir)
	require.NotNil(t, cmd)

	msg := cmd()
	initMsg, ok := msg.(initPlanListMsg)
	require.True(t, ok)
	require.NoError(t, initMsg.err)
	assert.Len(t, initMsg.items, 1)
}

func TestLoadPlansCmd_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	cmd := loadPlansCmd(dir)
	msg := cmd()

	initMsg, ok := msg.(initPlanListMsg)
	require.True(t, ok)
	require.NoError(t, initMsg.err)
	assert.Empty(t, initMsg.items)
}
