package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// planItem implements list.DefaultItem for display in the plan selection list.
type planItem struct {
	path string // absolute path to the plan file
	name string // display name (filename without extension)
	desc string // first non-empty line from the plan file
}

// FilterValue returns the value used for fuzzy filtering.
func (p planItem) FilterValue() string { return p.name }

// Title returns the display name.
func (p planItem) Title() string { return p.name }

// Description returns the subtitle (first line of the plan file).
func (p planItem) Description() string { return p.desc }

// loadPlanItems scans a directory for .md files and creates list items.
// excludes files in completed/ subdirectory.
func loadPlanItems(plansDir string) ([]list.Item, error) {
	pattern := filepath.Join(plansDir, "*.md")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob plans: %w", err)
	}

	items := make([]list.Item, 0, len(files))
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".md")
		desc := readFirstLine(f)
		items = append(items, planItem{
			path: f,
			name: name,
			desc: desc,
		})
	}
	return items, nil
}

// readFirstLine returns the first non-empty, non-heading-marker line from a file.
// skips YAML frontmatter blocks (delimited by ---) and strips markdown heading prefixes.
func readFirstLine(path string) string {
	f, err := os.Open(path) //nolint:gosec // path comes from filepath.Glob
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inFrontmatter := false
	firstLine := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// handle YAML frontmatter: skip everything between opening and closing ---
		if line == "---" {
			if firstLine {
				inFrontmatter = true
				firstLine = false
				continue
			}
			if inFrontmatter {
				inFrontmatter = false
				continue
			}
		}
		firstLine = false
		if inFrontmatter || line == "" {
			continue
		}
		// strip markdown heading prefix
		line = strings.TrimLeft(line, "#")
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// newPlanList creates a configured bubbles/list model for plan selection.
func newPlanList(items []list.Item, width, height int) list.Model {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#00ff00")).
		BorderForeground(lipgloss.Color("#00ff00"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("#00aa00")).
		BorderForeground(lipgloss.Color("#00ff00"))

	l := list.New(items, delegate, width, height)
	l.Title = "select plan"
	l.SetStatusBarItemName("plan", "plans")
	l.SetShowHelp(true)
	l.DisableQuitKeybindings()
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00ff00")).
		Bold(true)

	return l
}

// initPlanListMsg is sent to initialize the plan list with loaded items.
type initPlanListMsg struct {
	items []list.Item
	err   error
}

// loadPlansCmd returns a tea.Cmd that loads plan files from the given directory.
func loadPlansCmd(plansDir string) tea.Cmd {
	return func() tea.Msg {
		items, err := loadPlanItems(plansDir)
		return initPlanListMsg{items: items, err: err}
	}
}
