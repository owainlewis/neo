package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/glamour/v2"

	"github.com/owainlewis/neo/internal/agent"
)

func TestTextBlockRenderTrimsMarkdownEdgeNewlines(t *testing.T) {
	md, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	out := textBlock{text: "Assistant response"}.render(80, md)
	if strings.HasPrefix(out, "\n") {
		t.Fatalf("rendered text starts with a newline: %q", out)
	}
	if strings.HasSuffix(out, "\n") {
		t.Fatalf("rendered text ends with a newline: %q", out)
	}
	if !strings.Contains(plain(out), "Assistant response") {
		t.Fatalf("rendered text missing content: %q", out)
	}
}

func TestMaxTurnsBlockRenderShowsLimitAndContinuationHint(t *testing.T) {
	out := plain(maxTurnsBlock{limit: 50}.render(80, nil))
	if want := "hit turn limit (50). Reply to continue."; !strings.Contains(out, want) {
		t.Fatalf("rendered block missing %q: %q", want, out)
	}
}

func TestApprovalBlockRenderTruncatesLongPreview(t *testing.T) {
	var lines []string
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("+line %02d", i))
	}

	out := plain(approvalBlock{req: testApproval("write_file", strings.Join(lines, "\n"))}.render(80, nil))

	if !strings.HasPrefix(strings.TrimLeft(out, " "), "approve write_file?  y / n") {
		t.Fatalf("approval prompt should stay first, got:\n%s", out)
	}
	if strings.Contains(out, "+line 79") {
		t.Fatalf("approval preview was not truncated:\n%s", out)
	}
	if !strings.Contains(out, "more lines hidden") {
		t.Fatalf("approval preview missing truncation note:\n%s", out)
	}
}

func TestApprovalBlockRenderKeepsShortPreview(t *testing.T) {
	out := plain(approvalBlock{req: testApproval("edit_file", "-old\n+new")}.render(80, nil))
	for _, want := range []string{"approve edit_file?  y / n", "-old", "+new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("approval block missing %q:\n%s", want, out)
		}
	}
}

func testApproval(tool, preview string) agent.ApprovalRequest {
	return agent.ApprovalRequest{ToolName: tool, Preview: preview}
}
