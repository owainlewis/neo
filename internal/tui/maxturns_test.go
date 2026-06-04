package tui

import (
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
