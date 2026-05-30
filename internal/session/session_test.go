package session

import (
	"context"
	"errors"
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

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != sess.Metadata.ID {
		t.Fatalf("unexpected list: %#v", items)
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
