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
		cmd := strings.TrimSpace(stringArg(args, "command"))
		return "running " + truncate(oneLine(cmd), 60)
	case "read_file":
		return "reading " + shortPath(stringArg(args, "path"))
	case "write_file":
		return "writing " + shortPath(stringArg(args, "path"))
	case "edit_file":
		return "editing " + shortPath(stringArg(args, "path"))
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

