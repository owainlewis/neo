package workflow

import (
	"context"
	"testing"
)

func TestToolCreateEmitsWorkflow(t *testing.T) {
	events := make(chan Event, 1)
	tool := Tool{Events: events}

	out, err := tool.Run(context.Background(), map[string]any{
		"action": "create",
		"title":  "Release",
		"items": []any{
			map[string]any{"id": "1", "text": "Review code"},
			map[string]any{"id": "2", "text": "Run tests"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if out == "" {
		t.Fatal("expected JSON acknowledgement")
	}

	select {
	case ev := <-events:
		if ev.Action != "create" {
			t.Fatalf("action = %q, want create", ev.Action)
		}
		if ev.State.Title != "Release" {
			t.Fatalf("title = %q, want Release", ev.State.Title)
		}
		if len(ev.State.Items) != 2 {
			t.Fatalf("items = %d, want 2", len(ev.State.Items))
		}
		if ev.State.Items[0].Status != Pending {
			t.Fatalf("status = %q, want pending", ev.State.Items[0].Status)
		}
	default:
		t.Fatal("expected workflow event")
	}
}

func TestToolRequiresIDForStatusActions(t *testing.T) {
	tool := Tool{Events: make(chan Event, 1)}
	if _, err := tool.Run(context.Background(), map[string]any{"action": "start"}); err == nil {
		t.Fatal("expected missing id error")
	}
}
