package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleTool    = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	styleArg     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	styleFail    = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styleRetry   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleSpinner = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	styleResult  = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
)

// summarizeTool returns a one-line "name(args)" preview for display.
func summarizeTool(name string, args map[string]any) string {
	label := styleTool.Render(name)
	detail := toolArgPreview(name, args)
	if detail == "" {
		return label
	}
	return label + styleArg.Render("("+detail+")")
}

func toolArgPreview(name string, args map[string]any) string {
	switch name {
	case "bash":
		return truncate(oneLine(stringArg(args, "command")), 80)
	case "read_file", "write_file", "edit_file":
		return stringArg(args, "path")
	}
	for _, v := range args {
		if s, ok := v.(string); ok {
			return truncate(oneLine(s), 60)
		}
	}
	return ""
}

// summarizeResult returns a dim, single-line preview of a tool's output.
func summarizeResult(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return styleResult.Render("(no output)")
	}
	lines := strings.Count(t, "\n") + 1
	first := oneLine(t)
	if lines > 1 {
		return styleResult.Render(fmt.Sprintf("%s  +%d lines", truncate(first, 70), lines-1))
	}
	return styleResult.Render(truncate(first, 90))
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
