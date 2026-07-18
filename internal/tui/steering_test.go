package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/agent"
)

func TestBusyEnterSteersActiveTurn(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	var steered string
	m.steer = func(text string) bool {
		steered = text
		return true
	}
	m.input.SetValue("inspect the fallback")

	m.Update(keyPress(tea.KeyEnter))

	if steered != "inspect the fallback" {
		t.Fatalf("steering text = %q", steered)
	}
	if m.input.Value() != "" {
		t.Fatalf("composer was not cleared: %q", m.input.Value())
	}
	if got := plain(m.viewportContent()); !strings.Contains(got, "inspect the fallback") || !strings.Contains(got, "steering current turn") {
		t.Fatalf("steering state is not visible:\n%s", got)
	}
}

func TestRejectedSteeringStaysInComposer(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.steer = func(string) bool { return false }
	m.input.SetValue("too late")

	m.Update(keyPress(tea.KeyEnter))

	if got := m.input.Value(); got != "too late" {
		t.Fatalf("composer = %q, want rejected steering retained", got)
	}
	if got := plain(m.viewportContent()); !strings.Contains(got, "operation cannot be steered") {
		t.Fatalf("rejection is not visible:\n%s", got)
	}
}

func TestCtrlEnterQueuesOneFollowUpAndStartsItAfterTurn(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	m.input.SetValue("review the result")

	m.Update(ctrlEnter())

	if m.queued == nil || m.queued.agentText != "review the result" {
		t.Fatalf("queued turn = %#v", m.queued)
	}
	if m.input.Value() != "" {
		t.Fatalf("composer was not cleared: %q", m.input.Value())
	}
	if got := plain(m.statusLine()); !strings.Contains(got, "next queued") {
		t.Fatalf("queued status is not visible: %q", got)
	}

	m.Update(sendResultMsg{})

	if m.queued != nil {
		t.Fatalf("queued turn was not consumed: %#v", m.queued)
	}
	if !m.busy {
		t.Fatal("queued follow-up did not become the active turn")
	}
	if got := plain(m.viewportContent()); !strings.Contains(got, "starting queued follow-up") || !strings.Contains(got, "review the result") {
		t.Fatalf("follow-up transition is not visible:\n%s", got)
	}
}

func TestEscapeCancelsAndReturnsQueuedFollowUpToComposer(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	canceled := false
	m.sendCancel = func() { canceled = true }
	m.queued = &queuedTurn{displayText: "run the review", agentText: "run the review"}

	m.Update(keyPress(tea.KeyEsc))
	if !canceled {
		t.Fatal("escape did not cancel the active turn")
	}
	m.Update(sendResultMsg{err: context.Canceled})

	if m.busy {
		t.Fatal("canceled turn remained busy")
	}
	if m.queued != nil {
		t.Fatalf("canceled queue was not cleared: %#v", m.queued)
	}
	if got := m.input.Value(); got != "run the review" {
		t.Fatalf("composer = %q, want queued follow-up restored", got)
	}
	if got := plain(m.viewportContent()); !strings.Contains(got, "turn canceled") || !strings.Contains(got, "returned to the composer") {
		t.Fatalf("cancel transition is not visible:\n%s", got)
	}
}

func TestFailedTurnReturnsQueuedFollowUpToComposer(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	m.queued = &queuedTurn{displayText: "review the result", agentText: "review the result"}

	m.Update(sendResultMsg{err: context.DeadlineExceeded})

	if m.busy {
		t.Fatal("failed turn started the queued follow-up")
	}
	if m.queued != nil {
		t.Fatalf("failed queue was not cleared: %#v", m.queued)
	}
	if got := m.input.Value(); got != "review the result" {
		t.Fatalf("composer = %q, want queued follow-up restored", got)
	}
}

func TestFailedTurnRecoversInputInSubmissionOrder(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	m.pendingSteering = []string{"steer first"}
	m.queued = &queuedTurn{displayText: "follow up second", agentText: "follow up second"}
	m.input.SetValue("draft third")

	m.Update(sendResultMsg{err: context.DeadlineExceeded})

	if got, want := m.input.Value(), "steer first\nfollow up second\ndraft third"; got != want {
		t.Fatalf("composer = %q, want %q", got, want)
	}
}

func TestCancellationReturnsOnlyUnappliedSteering(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	m.pendingSteering = []string{"first", "second"}

	m.Update(agentEventMsg{ev: agent.Event{Kind: agent.EventSteeringApplied, Text: "first"}})
	m.Update(sendResultMsg{err: context.Canceled})

	if got := m.input.Value(); got != "second" {
		t.Fatalf("composer = %q, want only unapplied steering", got)
	}
	if len(m.pendingSteering) != 0 {
		t.Fatalf("pending steering was not cleared: %#v", m.pendingSteering)
	}
}

func TestCommandsCannotBeQueued(t *testing.T) {
	for _, input := range []string{"/review diff", "!git status"} {
		t.Run(input, func(t *testing.T) {
			m := makeTestModel()
			m.busy = true
			m.input.SetValue(input)

			m.Update(ctrlEnter())

			if m.queued != nil {
				t.Fatalf("command was queued: %#v", m.queued)
			}
			if got := m.input.Value(); got != input {
				t.Fatalf("composer = %q, want command retained", got)
			}
			if got := plain(m.viewportContent()); !strings.Contains(got, "commands cannot be queued") {
				t.Fatalf("queue rejection is not visible:\n%s", got)
			}
		})
	}
}

func TestEmptyQueueShortcutIsIgnored(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.input.SetValue("   ")

	m.Update(ctrlEnter())

	if m.queued != nil {
		t.Fatalf("empty input queued a follow-up: %#v", m.queued)
	}
}

func ctrlEnter() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl})
}

func (m *model) viewportContent() string {
	return m.viewport.View()
}
