package tui

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// imageExts are the file extensions we treat as attachable images. The model
// only accepts these; anything else stays as plain text in the message.
var imageExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
}

// dragPathToken matches a path-ish token in dragged/pasted input: either a
// file:// URL, a quoted path, or a bare path with backslash-escaped spaces
// (how Terminal.app and iTerm2 emit a dragged file). It is deliberately
// greedy about backslash-escaped spaces so "/My Photos/a b.png" survives.
var dragPathToken = regexp.MustCompile(`file://\S+|'[^']+'|"[^"]+"|(?:[^\s\\]|\\.)+`)

// extractImagePaths scans input for tokens that resolve to existing image
// files and returns the remaining message text plus the image paths it pulled
// out. Tokens that aren't images, or that don't point at a real file, are left
// untouched in the returned text. This is what turns "drag an image into the
// box" — which most terminals deliver as a pasted path — into an attachment.
func extractImagePaths(input string) (text string, paths []string) {
	var kept []string
	for _, tok := range dragPathToken.FindAllString(input, -1) {
		p, ok := imageCandidate(tok)
		if !ok {
			kept = append(kept, tok)
			continue
		}
		paths = append(paths, p)
	}
	text = strings.TrimSpace(strings.Join(kept, " "))
	return text, paths
}

// imageCandidate normalizes a single token and reports whether it points at an
// existing file with an image extension.
func imageCandidate(tok string) (string, bool) {
	p := strings.TrimSpace(tok)
	if p == "" {
		return "", false
	}
	// file:// URL (some terminals emit this on drag).
	if strings.HasPrefix(p, "file://") {
		if u, err := url.Parse(p); err == nil {
			p = u.Path
		}
	}
	// Strip surrounding quotes.
	if len(p) >= 2 {
		if (p[0] == '\'' && p[len(p)-1] == '\'') || (p[0] == '"' && p[len(p)-1] == '"') {
			p = p[1 : len(p)-1]
		}
	}
	// Unescape backslash-escaped characters (spaces, parens) from drag input.
	p = strings.NewReplacer(`\ `, " ", `\(`, "(", `\)`, ")", `\&`, "&").Replace(p)
	// Expand a leading ~ to the home directory.
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !imageExts[strings.ToLower(filepath.Ext(p))] {
		return "", false
	}
	if info, err := os.Stat(p); err != nil || info.IsDir() {
		return "", false
	}
	return p, true
}
