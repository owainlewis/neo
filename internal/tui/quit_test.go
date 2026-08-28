package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
)

type cancelBlockingProvider struct {
	started chan struct{}
}

func (p *cancelBlockingProvider) Name() string { return "cancel-blocking" }

func (p *cancelBlockingProvider) Complete(ctx context.Context, _ llm.Request) (*llm.Response, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestQuitDuringActiveTurnWaitsForPersistence(t *testing.T) {
	provider := &cancelBlockingProvider{started: make(chan struct{})}
	saveStarted := make(chan struct{})
	allowSave := make(chan struct{})
	m := makeTestModel()
	m.ag = agent.New(agent.Config{Model: "test", Provider: provider})
	m.afterSend = func() error {
		close(saveStarted)
		<-allowSave
		return nil
	}

	send := m.submitUserTurn("hello", "hello", nil)
	result := make(chan tea.Msg, 1)
	go func() { result <- send() }()
	waitForSignal(t, provider.started, "provider start")

	ctrlC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	if _, cmd := m.Update(ctrlC); cmd != nil {
		t.Fatal("first interrupt returned a quit command before persistence")
	}
	if !m.quitPending || m.quitting {
		t.Fatalf("quit state after first interrupt = pending %t, quitting %t; want true, false", m.quitPending, m.quitting)
	}

	waitForSignal(t, saveStarted, "session save start")
	select {
	case <-result:
		t.Fatal("send completed while session persistence was blocked")
	default:
	}
	if m.quitting {
		t.Fatal("model entered final quitting state before persistence completed")
	}

	close(allowSave)
	msg := receiveWithin(t, result, "send result")
	_, quit := m.Update(msg)
	assertQuitCommand(t, quit)
	if !m.quitting || m.busy {
		t.Fatalf("final quit state = quitting %t, busy %t; want true, false", m.quitting, m.busy)
	}
}

func TestQuitSaveFailureStaysOpenAndCanRetry(t *testing.T) {
	provider := &cancelBlockingProvider{started: make(chan struct{})}
	saveFailure := errors.New("disk unavailable")
	saveCalls := 0
	m := makeTestModel()
	m.ag = agent.New(agent.Config{Model: "test", Provider: provider})
	m.afterSend = func() error {
		saveCalls++
		if saveCalls == 1 {
			return saveFailure
		}
		return nil
	}

	send := m.submitUserTurn("hello", "hello", nil)
	result := make(chan tea.Msg, 1)
	go func() { result <- send() }()
	waitForSignal(t, provider.started, "provider start")

	ctrlC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	if _, cmd := m.Update(ctrlC); cmd != nil {
		t.Fatal("first interrupt returned a quit command")
	}
	resultMsg, ok := receiveWithin(t, result, "failed-save send result").(sendResultMsg)
	if !ok {
		t.Fatalf("send command returned an unexpected result type")
	}
	if !errors.Is(resultMsg.err, context.Canceled) {
		t.Fatalf("turn error = %v, want context.Canceled", resultMsg.err)
	}
	if !resultMsg.saveAttempted || !errors.Is(resultMsg.saveErr, saveFailure) {
		t.Fatalf("save outcome = attempted %t, error %v; want true, %v", resultMsg.saveAttempted, resultMsg.saveErr, saveFailure)
	}
	_, _ = m.Update(resultMsg)
	if m.quitting || m.quitPending {
		t.Fatalf("quit state after save failure = pending %t, quitting %t; want false, false", m.quitPending, m.quitting)
	}
	if !errors.Is(m.persistenceErr, saveFailure) {
		t.Fatalf("persistence error = %v, want %v", m.persistenceErr, saveFailure)
	}
	rendered := renderBlocks(m)
	if !strings.Contains(rendered, "save session: disk unavailable") || !strings.Contains(rendered, "quit canceled") {
		t.Fatalf("save failure was not explained to the user:\n%s", rendered)
	}

	_, retry := m.Update(ctrlC)
	if retry == nil {
		t.Fatal("retry quit did not return a persistence command")
	}
	retryResult := retry()
	if _, ok := retryResult.(persistenceRetryResultMsg); !ok {
		t.Fatalf("retry command returned %T, want persistenceRetryResultMsg", retryResult)
	}
	_, quit := m.Update(retryResult)
	assertQuitCommand(t, quit)
	if saveCalls != 2 {
		t.Fatalf("save calls = %d, want 2", saveCalls)
	}
	if m.persistenceErr != nil {
		t.Fatalf("persistence error after retry = %v, want nil", m.persistenceErr)
	}
}

func TestQuitDuringDirectCommandDoesNotBypassStalePersistenceFailure(t *testing.T) {
	m := makeTestModel()
	saveFailure := errors.New("previous save failed")
	m.Update(sendResultMsg{saveAttempted: true, saveErr: saveFailure})
	if !errors.Is(m.persistenceErr, saveFailure) {
		t.Fatalf("persistence error = %v, want %v", m.persistenceErr, saveFailure)
	}
	saveCalls := 0
	m.afterSend = func() error {
		saveCalls++
		return nil
	}

	direct := m.handleBangCommand("!echo hello")
	if direct == nil || !m.busy {
		t.Fatal("direct command did not start")
	}
	ctrlC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	if _, cmd := m.Update(ctrlC); cmd != nil {
		t.Fatal("first interrupt returned a quit command")
	}
	_, _ = m.Update(direct())
	if m.quitting || m.quitPending {
		t.Fatalf("quit state after direct command = pending %t, quitting %t; want false, false", m.quitPending, m.quitting)
	}
	if !errors.Is(m.persistenceErr, saveFailure) {
		t.Fatalf("direct command cleared stale persistence error: %v", m.persistenceErr)
	}
	if saveCalls != 0 {
		t.Fatalf("save calls before retry = %d, want 0", saveCalls)
	}
	if rendered := renderBlocks(m); !strings.Contains(rendered, "quit canceled because the session was not saved") {
		t.Fatalf("stale save failure was not explained to the user:\n%s", rendered)
	}

	_, retry := m.Update(ctrlC)
	if retry == nil {
		t.Fatal("retry quit did not return a persistence command")
	}
	retryResult := retry()
	if _, ok := retryResult.(persistenceRetryResultMsg); !ok {
		t.Fatalf("retry command returned %T, want persistenceRetryResultMsg", retryResult)
	}
	_, quit := m.Update(retryResult)
	assertQuitCommand(t, quit)
	if saveCalls != 1 || m.persistenceErr != nil {
		t.Fatalf("retry outcome = calls %d, persistence error %v; want 1, nil", saveCalls, m.persistenceErr)
	}
}

func TestSecondInterruptForcesQuitDuringActiveTurn(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	cancelCalls := 0
	m.sendCancel = func() { cancelCalls++ }

	if cmd := m.requestQuit(); cmd != nil {
		t.Fatal("first interrupt returned a quit command")
	}
	if cancelCalls != 1 {
		t.Fatalf("cancel calls after first interrupt = %d, want 1", cancelCalls)
	}

	assertQuitCommand(t, m.requestQuit())
	if cancelCalls != 1 {
		t.Fatalf("cancel calls after forced quit = %d, want 1", cancelCalls)
	}
	if !m.quitting {
		t.Fatal("forced quit did not enter final quitting state")
	}
}

func TestIdleInterruptQuitsImmediately(t *testing.T) {
	m := makeTestModel()

	assertQuitCommand(t, m.requestQuit())
	if m.quitPending {
		t.Fatal("idle quit should not enter the pending state")
	}
	if !m.quitting {
		t.Fatal("idle quit did not enter final quitting state")
	}
}

func TestQuitDuringApprovalDeniesBeforeWaitingForTurn(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	reply := make(chan bool, 1)
	m.approval = &approvalState{reply: reply}
	cancelCalls := 0
	m.sendCancel = func() { cancelCalls++ }

	ctrlC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	if _, cmd := m.Update(ctrlC); cmd != nil {
		t.Fatal("first interrupt during approval returned a quit command")
	}
	if approved := receiveWithin(t, reply, "approval reply"); approved {
		t.Fatal("quit approved the pending tool")
	}
	if m.approval != nil {
		t.Fatal("approval remained pending after quit request")
	}
	if !m.quitPending || m.quitting {
		t.Fatalf("quit state = pending %t, quitting %t; want true, false", m.quitPending, m.quitting)
	}
	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", cancelCalls)
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	receiveWithin(t, ch, name)
}

func receiveWithin[T any](t *testing.T, ch <-chan T, name string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		var zero T
		return zero
	}
}

func renderBlocks(m *model) string {
	var rendered strings.Builder
	for _, block := range m.blocks {
		rendered.WriteString(plain(block.render(80, nil)))
		rendered.WriteByte('\n')
	}
	return rendered.String()
}

func assertQuitCommand(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected quit command, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("command returned %T, want tea.QuitMsg", msg)
	}
}

func TestSlashQuitExitsImmediately(t *testing.T) {
	for _, cmd := range []string{"/quit", "/exit"} {
		t.Run(cmd, func(t *testing.T) {
			m := makeTestModel()
			saved := false
			m.afterSend = func() error { saved = true; return nil }

			got := m.handleSlashCommand(cmd)

			if got == nil {
				t.Fatal("expected a quit command")
			}
			if _, ok := got().(tea.QuitMsg); !ok {
				t.Fatalf("command did not quit: %T", got())
			}
			if !m.quitting {
				t.Fatal("model should be marked quitting")
			}
			if !saved {
				t.Fatal("session should be saved on the way out")
			}
		})
	}
}

// The point of /quit is that it does not wait for a turn to unwind. ctrl+c
// cancels and then waits, so a tool ignoring its context leaves the UI stuck.
func TestSlashQuitDoesNotWaitForABlockedTurn(t *testing.T) {
	provider := &cancelBlockingProvider{started: make(chan struct{})}
	m := makeTestModel()
	m.ag = agent.New(agent.Config{Model: "test", Provider: provider})

	send := m.startSend("hang", "hang", nil)
	m.busy = true
	go send()
	<-provider.started

	got := m.handleSlashCommand("/quit")
	if got == nil {
		t.Fatal("expected a quit command")
	}
	if _, ok := got().(tea.QuitMsg); !ok {
		t.Fatalf("quit must not wait for the turn: %T", got())
	}
	if m.quitPending {
		t.Fatal("/quit leaves directly; it must not enter the pending-quit wait")
	}
}

// A save failure is reported but must not trap the user in the session.
func TestSlashQuitExitsEvenIfTheSaveFails(t *testing.T) {
	m := makeTestModel()
	m.afterSend = func() error { return errors.New("disk full") }

	got := m.handleSlashCommand("/quit")
	if got == nil {
		t.Fatal("expected a quit command")
	}
	if _, ok := got().(tea.QuitMsg); !ok {
		t.Fatalf("a failed save must not block the exit: %T", got())
	}
	if !strings.Contains(renderBlocks(m), "disk full") {
		t.Fatal("the save failure should still be reported")
	}
}
