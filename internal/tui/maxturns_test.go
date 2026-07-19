package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

// TestModel_SendResultDoesNotDuplicateMaxTurnsSummary reproduces the real
// max-turns flow: EventMaxTurnsReached appends a maxTurnsBlock, then
// sendResultMsg fires with the wrapped ErrMaxTurns. If the turn did tool
// work, resultSummary would otherwise also produce a second, misleading
// "Finished with issues" card.
func TestModel_SendResultDoesNotDuplicateMaxTurnsSummary(t *testing.T) {
	m := makeTestModel()
	m.turn = turnStats{tools: 3}

	m.handleEvent(agent.Event{Kind: agent.EventMaxTurnsReached, MaxTurns: 50, Err: agent.ErrMaxTurns})
	m.Update(sendResultMsg{err: fmt.Errorf("agent stopped: %w", agent.ErrMaxTurns)})

	if len(m.blocks) != 1 {
		t.Fatalf("expected only the maxTurnsBlock, got %d blocks: %#v", len(m.blocks), m.blocks)
	}
	if _, ok := m.blocks[0].(maxTurnsBlock); !ok {
		t.Fatalf("expected maxTurnsBlock, got %T", m.blocks[0])
	}
}

func TestResultSummaryTreatsMaxTurnsAsPause(t *testing.T) {
	m := makeTestModel()
	m.turn = turnStats{tools: 3, workflow: true}

	summary, ok := m.resultSummary(fmt.Errorf("agent stopped: %w", agent.ErrMaxTurns), 2*time.Second)
	if !ok {
		t.Fatal("expected summary")
	}
	if summary.failed {
		t.Fatal("max turns should not render as a failed summary")
	}
	if summary.label != "Paused" {
		t.Fatalf("label = %q, want Paused", summary.label)
	}
	if !strings.Contains(summary.detail, "reply to continue") {
		t.Fatalf("detail missing continuation hint: %q", summary.detail)
	}
}

func TestModel_SendResultShowsCancellationNoticeAndClearsBusy(t *testing.T) {
	m := makeTestModel()
	m.width = 80
	m.height = 24
	m.busy = true
	m.currentTool = &toolCallBlock{name: "bash"}
	m.layout()
	busyViewportHeight := m.viewport.Height()
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
	if got := m.viewport.Height(); got != busyViewportHeight {
		t.Fatalf("idle viewport height = %d, want unchanged %d with one-row status", got, busyViewportHeight)
	}
	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want cancellation notice", len(m.blocks))
	}
	nb, ok := m.blocks[0].(noticeBlock)
	if !ok || nb.text != "turn canceled" {
		t.Fatalf("block = %#v, want turn canceled notice", m.blocks[0])
	}
}
