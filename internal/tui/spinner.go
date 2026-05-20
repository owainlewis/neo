package tui

import (
	"math/rand/v2"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// Status spinner: a single dot that gently pulses on/off. Calm, not jittery.
// Slow cadence (~2 frames/sec) keeps it from competing with the status text.
var statusSpinner = spinner.Spinner{
	Frames: []string{"●", " "},
	FPS:    time.Second / 2,
}

// Playful captions, shown only after a turn has been thinking for a while with
// no active tool — i.e. the user is staring at the screen and deserves a wink.
var spinnerCaptions = []string{
	"Reticulating splines",
	"Reverse engineering the universe",
	"Mining bitcoins... kidding",
	"Untangling pointers",
	"Negotiating with the compiler",
	"Compiling thoughts",
	"Asking nicely",
	"Consulting the rubber duck",
	"Counting electrons",
	"Polishing pixels",
	"Petting the cat",
}

type rotateCaptionMsg struct{}

func rotateCaptionEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return rotateCaptionMsg{} })
}

func randomCaption() string {
	return spinnerCaptions[rand.IntN(len(spinnerCaptions))]
}

// toolVerb returns a present-tense status phrase for an in-flight tool call,
// e.g. "reading internal/ui/style.go" or "running go test ./...".
func toolVerb(name string, args map[string]any) string {
	switch name {
	case "bash":
		cmd := strings.TrimSpace(stringArgT(args, "command"))
		return "running " + truncateT(oneLineT(cmd), 60)
	case "read_file":
		return "reading " + shortPath(stringArgT(args, "path"))
	case "write_file":
		return "writing " + shortPath(stringArgT(args, "path"))
	case "edit_file":
		return "editing " + shortPath(stringArgT(args, "path"))
	}
	return name
}

// shortPath shortens a path for the status line: keeps the last two segments
// if the full path is long, otherwise returns as-is.
func shortPath(p string) string {
	if len(p) <= 50 {
		return p
	}
	dir, file := filepath.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	parent := filepath.Base(dir)
	if parent == "." || parent == "/" || parent == "" {
		return ".../" + file
	}
	return ".../" + parent + "/" + file
}

// Local copies of helpers also defined in blocks.go. Duplication is
// deliberate and temporary — see GH #22 for the dedupe plan.
func stringArgT(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func oneLineT(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	return strings.TrimSpace(s)
}

func truncateT(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
