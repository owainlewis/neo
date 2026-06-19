package main

import (
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
)

func TestChatSystemAdvertisesAgentToolWorkflowPattern(t *testing.T) {
	system, blocks := chatSystem(&config.Config{}, t.TempDir(), nil)
	if !strings.Contains(system, "agent tool") || !strings.Contains(system, "subagent prompts") {
		t.Fatalf("system prompt missing agent-tool workflow guidance:\n%s", system)
	}
	if len(blocks) == 0 || !blocks[0].Cache {
		t.Fatalf("base prompt should be cacheable: %+v", blocks)
	}
}
