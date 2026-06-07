package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestFilePicker_ShowsWorkspacePathSubstringMatches(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"internal/tui/model.go",
		"internal/tools/search.go",
		"README.md",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("@mod")
	m.updateFilePicker()

	if !m.files.visible {
		t.Fatal("expected file picker to be visible")
	}
	if len(m.files.matches) != 1 || m.files.matches[0] != "internal/tui/model.go" {
		t.Fatalf("expected model.go match, got %+v", m.files.matches)
	}
}

func TestFilePicker_SkipsHeavyDirectories(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"internal/tui/model.go",
		".git/config",
		"node_modules/pkg/index.js",
		"dist/app.js",
		"vendor/pkg/file.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("@")
	m.updateFilePicker()

	got := strings.Join(m.files.matches, "\n")
	for _, skipped := range []string{".git/config", "node_modules/pkg/index.js", "dist/app.js", "vendor/pkg/file.go"} {
		if strings.Contains(got, skipped) {
			t.Fatalf("expected %s to be skipped, got %q", skipped, got)
		}
	}
	if !strings.Contains(got, "internal/tui/model.go") {
		t.Fatalf("expected regular file match, got %q", got)
	}
}

func TestFilePicker_ArrowKeysCycleSelection(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"a/one.go",
		"b/two.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("@go")
	m.updateFilePicker()

	m.Update(keyPress(tea.KeyUp))
	if got := m.files.matches[m.files.selected]; got != "b/two.go" {
		t.Fatalf("expected up to wrap to b/two.go, got %s", got)
	}
	m.Update(keyPress(tea.KeyDown))
	if got := m.files.matches[m.files.selected]; got != "a/one.go" {
		t.Fatalf("expected down to wrap to a/one.go, got %s", got)
	}
}

func TestFilePicker_TabAcceptsHighlightedPath(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"a/one.go",
		"b/two.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("read @go")
	m.updateFilePicker()
	m.Update(keyPress(tea.KeyDown))
	m.Update(keyPress(tea.KeyTab))

	if got := m.input.Value(); got != "read @b/two.go" {
		t.Fatalf("expected selected path in input, got %q", got)
	}
	if m.files.visible {
		t.Fatal("expected picker to hide after accepting a path")
	}
}

func TestFilePicker_EnterAcceptsPathWithoutSubmitting(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"internal/tui/model.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("@mod")
	m.updateFilePicker()
	m.Update(keyPress(tea.KeyEnter))

	if got := m.input.Value(); got != "@internal/tui/model.go" {
		t.Fatalf("expected selected path in input, got %q", got)
	}
	if len(m.blocks) != 0 {
		t.Fatalf("expected input not to submit, got %d blocks", len(m.blocks))
	}
}

func TestFilePicker_EscapeDismissesUntilTokenChanges(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"internal/tui/model.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("@mod")
	m.updateFilePicker()

	m.Update(keyPress(tea.KeyEsc))
	if m.files.visible {
		t.Fatal("expected picker to hide after escape")
	}
	m.updateFilePicker()
	if m.files.visible {
		t.Fatal("expected picker to stay hidden for dismissed token")
	}
	m.input.SetValue("@mode")
	m.updateFilePicker()
	if !m.files.visible {
		t.Fatal("expected picker to reappear when token changes")
	}
}

func TestFilePicker_DoesNotOpenForEmailLikeText(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"example.com",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)

	for _, input := range []string{"owain@example.com", "mail owain@example"} {
		m.input.SetValue(input)
		m.updateFilePicker()
		if m.files.visible {
			t.Fatalf("expected picker to stay hidden for %q", input)
		}
	}
}

func TestFilePicker_RenderFitsNarrowWidth(t *testing.T) {
	width := 16
	picker := filePicker{
		visible: true,
		matches: []string{
			"internal/tui/very-long-file-name.go",
			"README.md",
		},
	}
	out := plain(renderFilePicker(width, picker))
	for _, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line exceeds width %d: got %d line %q\n%s", width, got, line, out)
		}
	}
}

func makeFilePickerTree(t *testing.T, paths []string) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range paths {
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
