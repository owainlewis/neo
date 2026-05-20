package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/flow"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type StatusPrinter struct {
	Out     io.Writer
	Verbose bool

	mu       sync.Mutex
	stopCh   chan struct{}
	lastTool string
}

func (p *StatusPrinter) Status(u flow.StatusUpdate) {
	p.stopSpinner()

	phase := styleHeader.Render(strings.ToUpper(u.Phase))
	round := ""
	if u.Round > 1 {
		round = styleDim.Render(fmt.Sprintf(" round %d", u.Round))
	}
	msg := ""
	if u.Message != "" {
		msg = styleDim.Render(" — " + u.Message)
	}

	switch u.Status {
	case flow.StatusInProgress:
		head := fmt.Sprintf("%s %s",
			phase,
			styleDim.Render(fmt.Sprintf("(%d/%d)", u.Index, u.Total)))
		fmt.Fprintf(p.Out, "\n▸ %s%s%s\n", head, round, msg)
		p.startSpinner(strings.ToUpper(u.Phase))
	case flow.StatusCompleted:
		fmt.Fprintf(p.Out, "  %s %s%s\n", styleOK.Render("✓"), phase, msg)
	case flow.StatusFailed:
		fmt.Fprintf(p.Out, "  %s %s%s\n", styleFail.Render("✗"), phase, msg)
	case flow.StatusRetrying:
		fmt.Fprintf(p.Out, "  %s %s%s\n", styleRetry.Render("↻"), phase, msg)
	}
}

func (p *StatusPrinter) Event(phase string, e agent.Event) {
	switch e.Kind {
	case agent.EventToolCall:
		p.stopSpinner()
		fmt.Fprintf(p.Out, "  %s %s\n", styleDim.Render("·"), summarizeTool(e.Name, e.Args))
		p.mu.Lock()
		p.lastTool = e.Name
		p.mu.Unlock()
		p.startSpinner(strings.ToUpper(phase))
	case agent.EventToolResult:
		p.stopSpinner()
		fmt.Fprintf(p.Out, "    %s\n", summarizeResult(e.Text))
		p.mu.Lock()
		p.lastTool = ""
		p.mu.Unlock()
		p.startSpinner(strings.ToUpper(phase))
	case agent.EventError:
		p.stopSpinner()
		fmt.Fprintf(p.Out, "  %s %v\n", styleErr.Render("!"), e.Err)
	case agent.EventAssistantText:
		if p.Verbose {
			p.stopSpinner()
			fmt.Fprintln(p.Out, e.Text)
		}
	}
}

// Thinking shows the spinner with the given label until Done is called.
func (p *StatusPrinter) Thinking(label string) { p.startSpinner(strings.ToUpper(label)) }

// Done clears any active spinner.
func (p *StatusPrinter) Done() { p.stopSpinner() }

func (p *StatusPrinter) startSpinner(label string) {
	p.mu.Lock()
	if p.stopCh != nil {
		p.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	p.stopCh = stop
	p.mu.Unlock()

	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			select {
			case <-stop:
				fmt.Fprint(p.Out, "\r\033[K")
				return
			case <-t.C:
				p.mu.Lock()
				tool := p.lastTool
				p.mu.Unlock()
				frame := styleSpinner.Render(spinnerFrames[i%len(spinnerFrames)])
				i++
				lbl := styleDim.Render(label)
				if tool != "" {
					fmt.Fprintf(p.Out, "\r\033[K  %s %s %s %s", frame, lbl, styleDim.Render("·"), styleTool.Render(tool))
				} else {
					fmt.Fprintf(p.Out, "\r\033[K  %s %s", frame, lbl)
				}
			}
		}
	}()
}

func (p *StatusPrinter) stopSpinner() {
	p.mu.Lock()
	stop := p.stopCh
	p.stopCh = nil
	p.mu.Unlock()
	if stop != nil {
		close(stop)
		time.Sleep(120 * time.Millisecond)
	}
}
