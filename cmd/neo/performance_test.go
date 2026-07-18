package main

import (
	"testing"

	"github.com/owainlewis/neo/internal/config"
)

const basePromptByteBudget = 2048

func TestChatSystemBasePromptSizeBudget(t *testing.T) {
	prompt, _ := chatSystem(&config.Config{}, "", nil)
	if len(prompt) > basePromptByteBudget {
		t.Fatalf("base prompt size = %d bytes, budget = %d", len(prompt), basePromptByteBudget)
	}
}

// BenchmarkChatSystem tracks the stable base prompt construction path. Project
// context is intentionally excluded because its size and I/O depend on the
// checkout running the benchmark.
func BenchmarkChatSystem(b *testing.B) {
	cfg := &config.Config{}
	prompt, _ := chatSystem(cfg, "", nil)
	b.ReportAllocs()

	for b.Loop() {
		prompt, blocks := chatSystem(cfg, "", nil)
		if len(prompt) == 0 || len(blocks) != 1 {
			b.Fatal("unexpected base prompt")
		}
	}
	b.ReportMetric(float64(len(prompt)), "prompt_bytes")
}
