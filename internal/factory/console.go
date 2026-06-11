package factory

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Console renders the supervisor's tree to a terminal as a live, in-place
// frame (redrawn on a ticker) and tees every event to events.jsonl. It is
// a consumer of the event stream only — agents never wait on it (the
// supervisor's send is non-blocking by construction).
type Console struct {
	sup    *Supervisor
	out    io.Writer
	tty    bool
	frames int // lines printed in the previous frame
}

// NewConsole prepares a console over w. Live in-place redraw is enabled
// only when w is the process's terminal.
func NewConsole(sup *Supervisor, w io.Writer) *Console {
	tty := false
	if f, ok := w.(*os.File); ok {
		if info, err := f.Stat(); err == nil {
			tty = info.Mode()&os.ModeCharDevice != 0
		}
	}
	return &Console{sup: sup, out: w, tty: tty}
}

// Watch consumes events until the channel closes: tees them to jsonlPath
// (best-effort; "" disables) and repaints the tree at most every interval.
// Call it in a goroutine; it returns when the supervisor closes Events.
func (c *Console) Watch(jsonlPath string, interval time.Duration) {
	var tee *os.File
	if jsonlPath != "" {
		if err := os.MkdirAll(filepath.Dir(jsonlPath), 0o755); err == nil {
			tee, _ = os.OpenFile(jsonlPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		}
		if tee != nil {
			defer tee.Close()
		}
	}
	enc := json.NewEncoder(io.Discard)
	if tee != nil {
		enc = json.NewEncoder(tee)
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case ev, open := <-c.sup.Events:
			if !open {
				c.Repaint()
				return
			}
			_ = enc.Encode(ev)
			if !c.tty {
				fmt.Fprintf(c.out, "[%s] %s %s: %s\n",
					ev.At.Format("15:04:05"), ev.Step, ev.Ev.Kind, clip(ev.Ev.Body, 120))
			}
		case <-tick.C:
			c.Repaint()
		}
	}
}

// Repaint draws the current tree. On a TTY it rewrites the previous frame
// in place; otherwise it is a no-op (the event log lines are the output).
func (c *Console) Repaint() {
	if !c.tty {
		return
	}
	frame := RenderTree(c.sup.Snapshot())
	if c.frames > 0 {
		fmt.Fprintf(c.out, "\x1b[%dA\x1b[J", c.frames) // up N lines, clear below
	}
	fmt.Fprint(c.out, frame)
	c.frames = strings.Count(frame, "\n")
}
