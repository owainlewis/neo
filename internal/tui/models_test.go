package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSlashCommand_ModelOpensSearchableBrowser(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2", Description: "flagship"},
		{ID: "gpt-4o", Name: "GPT-4o", Description: "fast"},
	})

	m.handleSlashCommand("/model")

	if !m.models.visible {
		t.Fatal("expected model browser to open")
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "Select a model") || !strings.Contains(out, "gpt-5.2") {
		t.Fatalf("model browser did not render choices: %s", out)
	}

	for _, r := range "4o" {
		m.handleModelBrowserKey(keyPress(r))
	}
	out = plain(m.View().Content)
	if !strings.Contains(out, "gpt-4o") || strings.Contains(out, "gpt-5.2") {
		t.Fatalf("model browser did not filter by query: %s", out)
	}
}

func TestModelBrowser_EnterSelectsModelAndSaves(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2"},
	})
	saveCalls := 0
	m.afterSend = func() error {
		saveCalls++
		return nil
	}

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if m.models.visible {
		t.Fatal("expected model browser to close after selection")
	}
	if got := m.ag.Model(); got != "test" {
		t.Fatalf("selected wrong model: got %q want test", got)
	}
	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))
	if got := m.ag.Model(); got != "gpt-5.2" {
		t.Fatalf("agent model = %q, want gpt-5.2", got)
	}
	if m.modelTag != "gpt-5.2" {
		t.Fatalf("modelTag = %q, want gpt-5.2", m.modelTag)
	}
	if saveCalls != 2 {
		t.Fatalf("saveCalls = %d, want 2", saveCalls)
	}
	out := plain(renderBlocks(m.blocks))
	if !strings.Contains(out, "model: gpt-5.2") {
		t.Fatalf("selection notice missing: %s", out)
	}
}

func TestModelBrowser_SaveErrorKeepsBrowserOpen(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{{ID: "gpt-5.2"}})
	m.afterSend = func() error { return fmt.Errorf("save failed") }

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if !m.models.visible {
		t.Fatal("expected model browser to stay open on save error")
	}
	if m.models.err == nil || !strings.Contains(m.models.err.Error(), "save failed") {
		t.Fatalf("expected save error, got %v", m.models.err)
	}
}
