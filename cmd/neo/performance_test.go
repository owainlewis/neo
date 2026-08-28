package main

import (
	"io"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/profile"
)

// Two budgets, because the prompt is no longer one size. The core is sent on
// every request in every mode and is the number that matters most; the chat
// budget covers it plus the workflow and delegation sections, which only exist
// where those tools do.
const (
	corePromptByteBudget = 1400
	chatPromptByteBudget = 2600
)

func TestChatSystemPromptSizeBudgets(t *testing.T) {
	headless, _ := chatSystem(&config.Config{}, "", nil, profile.Profile{}, newRegistry("", ""), io.Discard)
	if len(headless) > corePromptByteBudget {
		t.Fatalf("always-loaded prompt = %d bytes, budget = %d", len(headless), corePromptByteBudget)
	}
	chat, _ := chatSystem(&config.Config{}, "", nil, profile.Profile{}, chatRegistry(), io.Discard)
	if len(chat) > chatPromptByteBudget {
		t.Fatalf("chat prompt = %d bytes, budget = %d", len(chat), chatPromptByteBudget)
	}
}

// BenchmarkChatSystem tracks the stable base prompt construction path. Project
// context is intentionally excluded because its size and I/O depend on the
// checkout running the benchmark.
func BenchmarkChatSystem(b *testing.B) {
	cfg := &config.Config{}
	prompt, _ := chatSystem(cfg, "", nil, profile.Profile{}, chatRegistry(), io.Discard)
	b.ReportAllocs()

	for b.Loop() {
		prompt, blocks := chatSystem(cfg, "", nil, profile.Profile{}, chatRegistry(), io.Discard)
		if len(prompt) == 0 || len(blocks) != 1 {
			b.Fatal("unexpected base prompt")
		}
	}
	b.ReportMetric(float64(len(prompt)), "prompt_bytes")
}
