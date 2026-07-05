package tui

import (
	"context"
	"fmt"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
)

func TestModel_MaxTurnsEventAppendsDistinctBlock(t *testing.T) {
	m := makeTestModel()

	m.handleEvent(agent.Event{Kind: agent.EventMaxTurnsReached, MaxTurns: 50, Err: agent.ErrMaxTurns})

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	block, ok := m.blocks[0].(maxTurnsBlock)
	if !ok {
		t.Fatalf("expected maxTurnsBlock, got %T", m.blocks[0])
	}
	if block.limit != 50 {
		t.Fatalf("limit = %d, want 50", block.limit)
	}
}

func TestModel_SendResultSuppressesGenericMaxTurnsError(t *testing.T) {
	m := makeTestModel()

	m.Update(sendResultMsg{err: fmt.Errorf("agent stopped: %w", agent.ErrMaxTurns)})

	if len(m.blocks) != 0 {
		t.Fatalf("expected no generic error block, got %T", m.blocks[0])
	}
}

func TestModel_SendResultShowsCancellationNoticeAndClearsBusy(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.currentTool = &toolCallBlock{name: "bash"}
	canceled := false
	m.sendCancel = func() { canceled = true }

	m.Update(sendResultMsg{err: context.Canceled})

	if m.busy {
		t.Fatal("model stayed busy after cancellation")
	}
	if m.currentTool != nil {
		t.Fatalf("currentTool = %#v, want nil", m.currentTool)
	}
	if m.sendCancel != nil {
		t.Fatal("sendCancel was not cleared")
	}
	if !canceled {
		t.Fatal("send cancel cleanup was not called")
	}
	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want cancellation notice", len(m.blocks))
	}
	nb, ok := m.blocks[0].(noticeBlock)
	if !ok || nb.text != "turn canceled" {
		t.Fatalf("block = %#v, want turn canceled notice", m.blocks[0])
	}
}
