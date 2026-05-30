package tui

import "regexp"

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips ANSI escape codes so tests can assert on rendered text content.
func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// makeTestModel builds a minimal model for slash-command and state-transition
// tests without going through newModel (which probes the terminal). Only the
// fields exercised by tests are populated.
func makeTestModel() *model { return &model{} }
