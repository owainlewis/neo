package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

func TestStoreCreateSaveLoadList(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create(context.Background(), Metadata{
		Source: "tui",
		CWD:    "/repo",
		Model:  "test-model",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Metadata.ID == "" {
		t.Fatal("Create did not assign session id")
	}
	sess.Messages = []llm.Message{{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{{
			Type: "text",
			Text: "  summarize this repository\nplease  ",
		}},
	}}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(context.Background(), sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Metadata.Title != "summarize this repository please" {
		t.Fatalf("unexpected title: %q", loaded.Metadata.Title)
	}
	if got := loaded.Messages[0].Content[0].Text; got != "  summarize this repository\nplease  " {
		t.Fatalf("message text changed: %q", got)
	}
	sess.Usage = llm.Usage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save with usage: %v", err)
	}
	loaded, err = store.Load(context.Background(), sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load with usage: %v", err)
	}
	if loaded.Usage != sess.Usage {
		t.Fatalf("usage = %+v, want %+v", loaded.Usage, sess.Usage)
	}
	b, err := os.ReadFile(store.sessionPath(sess.Metadata.ID))
	if err != nil {
		t.Fatalf("read session json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("parse session json: %v", err)
	}
	usage, ok := raw["usage"].(map[string]any)
	if !ok {
		t.Fatalf("session json missing usage object: %s", b)
	}
	for _, key := range []string{"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens"} {
		if _, ok := usage[key]; !ok {
			t.Fatalf("usage json missing %q: %v", key, usage)
		}
	}

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != sess.Metadata.ID {
		t.Fatalf("unexpected list: %#v", items)
	}
}

func TestStoreLoadOlderSessionWithoutUsage(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const body = `{
  "metadata": {
    "id": "sess_old",
    "source": "tui",
    "cwd": "/repo",
    "model": "test",
    "created_at": "2026-01-01T00:00:00Z",
    "updated_at": "2026-01-01T00:00:00Z"
  },
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "hello"}]}
  ]
}`
	if err := os.WriteFile(store.sessionPath("sess_old"), []byte(body), 0o600); err != nil {
		t.Fatalf("write old session: %v", err)
	}
	loaded, err := store.Load(context.Background(), "sess_old")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Usage != (llm.Usage{}) {
		t.Fatalf("usage = %+v, want zero", loaded.Usage)
	}
	if got := loaded.Messages[0].Content[0].Text; got != "hello" {
		t.Fatalf("message text = %q", got)
	}
}

func TestStoreListNewestFirst(t *testing.T) {
	store := NewStore(t.TempDir())
	older := &Session{Metadata: Metadata{ID: "older", Source: "tui", CreatedAt: time.Now().Add(-time.Hour)}}
	newer := &Session{Metadata: Metadata{ID: "newer", Source: "tui", CreatedAt: time.Now()}}
	if err := store.Save(context.Background(), older); err != nil {
		t.Fatalf("save older: %v", err)
	}
	time.Sleep(time.Millisecond)
	if err := store.Save(context.Background(), newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}
	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].ID != "newer" || items[1].ID != "older" {
		t.Fatalf("expected newest first, got %#v", items)
	}
}

func TestStoreFindByExternal(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.Create(context.Background(), Metadata{Source: "telegram", External: "chat:123"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	found, err := store.FindByExternal(context.Background(), "telegram", "chat:123")
	if err != nil {
		t.Fatalf("FindByExternal: %v", err)
	}
	if found.Metadata.Source != "telegram" || found.Metadata.External != "chat:123" {
		t.Fatalf("unexpected session: %#v", found.Metadata)
	}
	_, err = store.FindByExternal(context.Background(), "telegram", "chat:missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreDelete(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create(context.Background(), Metadata{Source: "tui"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(context.Background(), sess.Metadata.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = store.Load(context.Background(), sess.Metadata.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty list, got %#v", items)
	}
}

func TestTitleFromTextTruncates(t *testing.T) {
	long := "one two\n" + string(make([]byte, 100))
	got := TitleFromText(long)
	if got == "" || len(got) > 82 {
		t.Fatalf("unexpected title %q len=%d", got, len(got))
	}
}
