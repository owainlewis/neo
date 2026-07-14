package tui

import (
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
)

// Status spinner: a dot that gently changes weight but never disappears.
// Keeping a visible mark in every frame makes a slow provider response look
// active instead of stalled.
var statusSpinner = spinner.Spinner{
	Frames: []string{"●", "◉"},
	FPS:    time.Second / 2,
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
	case "grep":
		return "searching " + truncate(oneLine(stringArg(args, "pattern")), 60)
	case "glob":
		return "matching " + truncate(oneLine(stringArg(args, "pattern")), 60)
	case "agent":
		return "agent " + truncate(oneLine(stringArg(args, "prompt")), 40)
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
