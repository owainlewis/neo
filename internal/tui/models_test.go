package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
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
	m.persistenceErr = fmt.Errorf("stale save failure")
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
	if m.persistenceErr != nil {
		t.Fatalf("persistence error after successful model save = %v, want nil", m.persistenceErr)
	}
	out := plain(m.viewportContent())
	if !strings.Contains(out, "model: gpt-5.2") {
		t.Fatalf("selection notice missing: %s", out)
	}
}

func TestModelBrowser_SaveErrorKeepsBrowserOpen(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{{ID: "gpt-5.2"}})
	saveErr := fmt.Errorf("save failed")
	m.afterSend = func() error { return saveErr }

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if !m.models.visible {
		t.Fatal("expected model browser to stay open on save error")
	}
	if m.models.err == nil || !strings.Contains(m.models.err.Error(), "save failed") {
		t.Fatalf("expected save error, got %v", m.models.err)
	}
	if !errors.Is(m.persistenceErr, saveErr) {
		t.Fatalf("persistence error = %v, want %v", m.persistenceErr, saveErr)
	}
}

func TestModelBrowser_UsesSwitcher(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "test"},
		{ID: "claude-sonnet-4-6"},
	})
	var selected string
	m.modelSwitcher = func(model string) error {
		selected = model
		return nil
	}

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if selected != "claude-sonnet-4-6" {
		t.Fatalf("selected = %q", selected)
	}
	if m.providerTag != "anthropic" || m.modelTag != "claude-sonnet-4-6" {
		t.Fatalf("visible backend = %s/%s", m.providerTag, m.modelTag)
	}
	if got := plain(m.footerLine()); !strings.Contains(got, "anthropic/claude-sonnet-4-6") {
		t.Fatalf("footer does not show provider and model: %q", got)
	}
}

func TestModelBrowser_SwitchFailureKeepsCurrentBackend(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "test"},
		{ID: "claude-sonnet-4-6"},
	})
	m.modelSwitcher = func(string) error { return fmt.Errorf("switch failed") }

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if !m.models.visible || m.models.err == nil {
		t.Fatal("failed switch should keep the picker open with an error")
	}
	if m.providerTag != "anthropic" || m.modelTag != "test" {
		t.Fatalf("model changed after failed switch: %s/%s", m.providerTag, m.modelTag)
	}
}

func TestModelBrowser_LazyLoadAndSessionCache(t *testing.T) {
	for _, closeBeforeResult := range []bool{false, true} {
		t.Run(fmt.Sprint(closeBeforeResult), func(t *testing.T) {
			m := makeTestModel()
			calls := 0
			warning := errors.New("catalogue unavailable; using default")
			m.modelLoader = func(ctx context.Context) ([]ModelChoice, error) {
				calls++
				return []ModelChoice{{ID: "fallback"}}, warning
			}
			if cmd := m.Init(); cmd != nil || calls != 0 {
				t.Fatal("startup must not load models")
			}
			cmd := m.handleSlashCommand("/model")
			if calls != 0 || !m.modelsLoading || !m.models.visible {
				t.Fatal("expected deferred load")
			}
			if out := plain(m.View().Content); !strings.Contains(out, "Loading models") {
				t.Fatalf("missing loading indicator: %s", out)
			}
			m.handleModelBrowserKey(keyPress(tea.KeyEnter))
			if !m.models.visible {
				t.Fatal("enter must not select while loading")
			}
			batch := cmd().(tea.BatchMsg)
			if closeBeforeResult {
				m.handleModelBrowserKey(keyPress(tea.KeyEscape))
				if m.models.visible {
					t.Fatal("escape must close while loading")
				}
				m.handleSlashCommand("/model")
			}
			// Execute the fetch command separately from the spinner tick.
			m.Update(batch[1]())
			if calls != 1 || m.modelsLoading || !m.modelsLoaded {
				t.Fatal("load did not complete exactly once")
			}
			if out := plain(m.View().Content); !strings.Contains(out, "fallback") || !strings.Contains(out, warning.Error()) || strings.Contains(out, "Loading models") {
				t.Fatalf("missing loaded result: %s", out)
			}
			if m.modelChoices[0].ID != "test" {
				t.Fatal("configured model must remain selectable")
			}
			m.closeModelBrowser()
			if cmd := m.handleSlashCommand("/model"); cmd != nil || calls != 1 {
				t.Fatal("reopening must use cached result")
			}
		})
	}
}

func TestModelBrowser_LoadCompletesWhileClosed(t *testing.T) {
	m := makeTestModel()
	m.modelLoader = func(context.Context) ([]ModelChoice, error) { return []ModelChoice{{ID: "loaded"}}, nil }
	batch := m.handleSlashCommand("/model")().(tea.BatchMsg)
	m.closeModelBrowser()
	m.Update(batch[1]())
	if m.models.visible {
		t.Fatal("completion must not reopen picker")
	}
	if cmd := m.openModelBrowser(); cmd != nil {
		t.Fatal("completed choices should be cached")
	}
	if out := plain(m.View().Content); !strings.Contains(out, "loaded") {
		t.Fatalf("missing cached model: %s", out)
	}
}

func TestModelBrowser_ReopenReusesPendingSpinner(t *testing.T) {
	m := makeTestModel()
	m.modelSpin = spinner.New(spinner.WithSpinner(spinner.Dot))
	m.modelLoader = func(context.Context) ([]ModelChoice, error) { return nil, nil }
	batch := m.openModelBrowser()().(tea.BatchMsg)
	pending := batch[0]
	for range 3 {
		m.closeModelBrowser()
		if cmd := m.openModelBrowser(); cmd != nil {
			t.Fatal("reopening with a pending tick must not start another spinner")
		}
		_, pending = m.Update(pending())
		if pending == nil {
			t.Fatal("pending tick must continue animation after reopening")
		}
	}

	m.closeModelBrowser()
	if _, cmd := m.Update(pending()); cmd != nil {
		t.Fatal("tick while closed must stop animation")
	}
	pending = m.openModelBrowser()
	if pending == nil {
		t.Fatal("reopening after animation stops must restart it")
	}
	_, pending = m.Update(pending())
	if pending == nil {
		t.Fatal("restarted spinner must continue animation")
	}
	m.Update(batch[1]())
	if _, cmd := m.Update(pending()); cmd != nil {
		t.Fatal("load completion must stop animation")
	}
}
