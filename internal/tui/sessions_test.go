package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/session"
)

func TestSlashCommand_SessionsOpensSearchableBrowser(t *testing.T) {
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	saveTestSession(t, store, session.Metadata{ID: "sess_auth", CWD: cwd, Source: "tui", Model: "test", Title: "Auth work"}, "auth notes")
	saveTestSession(t, store, session.Metadata{ID: "sess_docs", CWD: cwd, Source: "tui", Model: "test", Title: "Docs work"}, "docs notes")

	m := makeTestModel()
	m.sessionStore = store
	m.currentSessionCWD = cwd
	m.handleSlashCommand("/sessions")

	if !m.sessions.visible {
		t.Fatal("expected sessions browser to open")
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "Resume a previous session") || !strings.Contains(out, "Auth work") {
		t.Fatalf("browser did not render sessions: %s", out)
	}

	for _, r := range "docs" {
		m.handleSessionBrowserKey(keyPress(r))
	}
	out = plain(m.View().Content)
	if !strings.Contains(out, "Docs work") || strings.Contains(out, "Auth work") {
		t.Fatalf("browser did not filter by query: %s", out)
	}
}

func TestSessionBrowser_EnterResumesSelectedSession(t *testing.T) {
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	sess := saveTestSession(t, store, session.Metadata{ID: "sess_old", CWD: cwd, Source: "tui", Model: "test", Title: "Old task"}, "continue this")
	sess.Usage = llm.Usage{InputTokens: 7, OutputTokens: 8, CacheCreationTokens: 9, CacheReadTokens: 10}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("save usage: %v", err)
	}

	m := makeTestModel()
	m.sessionStore = store
	m.currentSessionCWD = cwd
	var resumed *session.Session
	m.onSessionResume = func(s *session.Session) { resumed = s }

	m.handleSlashCommand("/sessions")
	m.handleSessionBrowserKey(keyPress(tea.KeyEnter))

	if m.sessions.visible {
		t.Fatal("expected sessions browser to close after resume")
	}
	if resumed == nil || resumed.Metadata.ID != sess.Metadata.ID {
		t.Fatalf("resume callback got %#v", resumed)
	}
	if m.currentSessionID != sess.Metadata.ID {
		t.Fatalf("currentSessionID = %q, want %q", m.currentSessionID, sess.Metadata.ID)
	}
	got := m.ag.Transcript()
	if len(got) != 1 || got[0].Content[0].Text != "continue this" {
		t.Fatalf("agent transcript not replaced: %#v", got)
	}
	if got := m.ag.Usage(); got != sess.Usage {
		t.Fatalf("agent usage = %+v, want %+v", got, sess.Usage)
	}
	out := plain(renderBlocks(m.blocks))
	if !strings.Contains(out, "resumed session: Old task") || !strings.Contains(out, "continue this") {
		t.Fatalf("resumed transcript not rendered: %s", out)
	}
}

func TestSessionBrowser_RejectsCrossCWDResume(t *testing.T) {
	currentCWD := t.TempDir()
	otherCWD := t.TempDir()
	store := session.NewStore(t.TempDir())
	saveTestSession(t, store, session.Metadata{ID: "sess_other", CWD: otherCWD, Source: "tui", Model: "test", Title: "Other repo"}, "other")

	m := makeTestModel()
	m.sessionStore = store
	m.currentSessionCWD = currentCWD

	m.handleSlashCommand("/sessions")
	m.handleSessionBrowserKey(keyPress(tea.KeyTab)) // switch from cwd to all.
	m.handleSessionBrowserKey(keyPress(tea.KeyEnter))

	if !m.sessions.visible {
		t.Fatal("expected browser to stay open")
	}
	if m.sessions.err == nil || !strings.Contains(m.sessions.err.Error(), "neo resume sess_other") {
		t.Fatalf("expected cross-cwd resume hint, got %v", m.sessions.err)
	}
	if len(m.ag.Transcript()) != 0 {
		t.Fatalf("cross-cwd resume changed transcript: %#v", m.ag.Transcript())
	}
}

func saveTestSession(t *testing.T, store *session.Store, meta session.Metadata, text string) *session.Session {
	t.Helper()
	sess := &session.Session{
		Metadata: meta,
		Messages: []llm.Message{{
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{{
				Type: "text",
				Text: text,
			}},
		}},
	}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return sess
}

func renderBlocks(blocks []block) string {
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(b.render(80, nil))
		sb.WriteString("\n")
	}
	return sb.String()
}
