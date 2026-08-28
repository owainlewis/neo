package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/profile"
	"github.com/owainlewis/neo/internal/subagent"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/workflow"
)

func TestSubagentBackendFollowsCoordinatorByDefault(t *testing.T) {
	fallback := &llmtest.FakeProvider{}
	prov, model, follows := subagentBackend(context.Background(), &config.Config{}, fallback, "main-model")
	if prov != fallback || model != "main-model" || !follows {
		t.Fatalf("backend = %T/%q follows=%t", prov, model, follows)
	}
}

func TestSubagentBackendUsesConfiguredProviderAndModel(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	cfg := &config.Config{Subagents: config.Backend{Provider: "anthropic", Model: "worker-model"}}
	prov, model, follows := subagentBackend(context.Background(), cfg, &llmtest.FakeProvider{}, "main-model")
	if prov.Name() != "anthropic" || model != "worker-model" || follows {
		t.Fatalf("backend = %s/%q follows=%t", prov.Name(), model, follows)
	}
}

func TestSubagentBackendCredentialFailureDoesNotBreakCoordinator(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	fallback := &llmtest.FakeProvider{}
	cfg := &config.Config{Subagents: config.Backend{Provider: "google", Model: "worker-model"}}
	prov, model, follows := subagentBackend(context.Background(), cfg, fallback, "main-model")
	if prov == fallback || model != "worker-model" || follows {
		t.Fatalf("backend = %T/%q follows=%t", prov, model, follows)
	}
	_, err := prov.Complete(context.Background(), llm.Request{Model: model})
	if err == nil {
		t.Fatal("expected unavailable worker backend to fail")
	}
	for _, want := range []string{"subagent backend", "google/worker-model", "GOOGLE_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestChatAgentToolPassesCompactionContextWindowToRunner(t *testing.T) {
	cfg := &config.Config{Compaction: config.Compaction{ContextWindowTokens: 1_000_000}}
	_, _, runner := chatAgentTool(&llmtest.FakeProvider{}, "worker-model", t.TempDir(), t.TempDir(), cfg)
	if runner.ContextWindowTokens != 1_000_000 {
		t.Fatalf("runner context window = %d, want 1000000", runner.ContextWindowTokens)
	}
}

// The prompt must describe a tool only where that tool exists. `neo run` builds
// the base registry with no workflow or agent tool, so instructing it to use
// them would be a contract the model cannot keep.
func TestChatSystemScopesCapabilityGuidanceToRegisteredTools(t *testing.T) {
	cfg := &config.Config{}
	capabilityPhrases := []string{"workflow tool", "agent tool", "subagent"}

	headless, _ := chatSystem(cfg, t.TempDir(), nil, profile.Profile{}, newRegistry("", ""), io.Discard)
	for _, phrase := range capabilityPhrases {
		if strings.Contains(headless, phrase) {
			t.Fatalf("headless prompt names %q but has no such tool:\n%s", phrase, headless)
		}
	}

	chat, blocks := chatSystem(cfg, t.TempDir(), nil, profile.Profile{}, chatRegistry(), io.Discard)
	for _, phrase := range capabilityPhrases {
		if !strings.Contains(chat, phrase) {
			t.Fatalf("chat prompt is missing %q:\n%s", phrase, chat)
		}
	}
	for _, want := range []string{"# Workflow checklist", "# Delegation"} {
		if !strings.Contains(chat, want) {
			t.Fatalf("chat prompt missing section %q:\n%s", want, chat)
		}
	}
	if len(blocks) == 0 || !blocks[0].Cache {
		t.Fatalf("capability sections belong in the cacheable base: %+v", blocks)
	}
}

// Every tool the prompt names must be in the registry that prompt was built for.
func TestChatSystemNamesNoUnregisteredTool(t *testing.T) {
	for _, tc := range []struct {
		name string
		reg  *tools.Registry
	}{
		{"headless", newRegistry("", "")},
		{"chat", chatRegistry()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			system, _ := chatSystem(&config.Config{}, t.TempDir(), nil, profile.Profile{}, tc.reg, io.Discard)
			registered := map[string]bool{}
			for _, n := range tc.reg.Names() {
				registered[n] = true
			}
			for _, candidate := range []string{"workflow", "agent"} {
				if strings.Contains(system, candidate+" tool") && !registered[candidate] {
					t.Fatalf("prompt names the %s tool, which is not registered", candidate)
				}
			}
		})
	}
}

func TestChatSystemAdvertisesNamedPhasesWithoutPromptBodies(t *testing.T) {
	system, _ := chatSystem(&config.Config{}, t.TempDir(), nil, profile.Profile{}, chatRegistry(), io.Discard)
	for _, want := range []string{"# Named phases", "/design", "/plan", "/build", "/review"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q named phase catalog:\n%s", want, system)
		}
	}
	if strings.Contains(system, "Design the requested change before implementation.") {
		t.Fatalf("system prompt included named phase body:\n%s", system)
	}
}

func TestChatSystemPreservesAgentsWorkflowInstructions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	const instructions = "Follow this workflow when changing code:\n1. Inspect the issue\n2. Implement the change\n3. Launch a review subagent"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(instructions), 0o644); err != nil {
		t.Fatal(err)
	}

	system, blocks := chatSystem(&config.Config{}, root, nil, profile.Profile{}, chatRegistry(), io.Discard)

	if len(blocks) < 2 {
		t.Fatalf("system blocks = %d, want project instructions block", len(blocks))
	}
	if !strings.Contains(system, instructions) {
		t.Fatalf("AGENTS.md workflow was not preserved:\n%s", system)
	}
}

func TestChatSystemWarnsAndExcludesEscapingAgentsSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	const global = "safe global instructions remain loaded"
	if err := os.MkdirAll(filepath.Join(home, ".neo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".neo", "AGENTS.md"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	const local = "safe local instructions remain loaded"
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "outside sentinel must never enter the system prompt"
	outside := filepath.Join(base, "outside-AGENTS.md")
	if err := os.WriteFile(outside, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	var warnings bytes.Buffer
	system, blocks := chatSystem(&config.Config{}, cwd, nil, profile.Profile{}, chatRegistry(), &warnings)

	if strings.Contains(system, sentinel) {
		t.Fatal("escaping AGENTS.md target entered the system prompt")
	}
	for _, safe := range []string{global, local} {
		if !strings.Contains(system, safe) {
			t.Fatalf("system prompt did not preserve %q", safe)
		}
	}
	if n := blocksContaining(blocks, "# Project instructions"); n != 1 {
		t.Fatalf("project instruction blocks = %d, want 1", n)
	}
	for _, want := range []string{"warning: AGENTS.md:", "outside workspace root"} {
		if !strings.Contains(warnings.String(), want) {
			t.Fatalf("warning %q does not contain %q", warnings.String(), want)
		}
	}
}

func TestChatSystemStatesTheEnvironment(t *testing.T) {
	cwd := t.TempDir()
	system, blocks := chatSystem(&config.Config{}, cwd, nil, profile.Profile{}, chatRegistry(), io.Discard)

	for _, want := range []string{"# Environment", "Working directory: " + cwd, "Platform: " + runtime.GOOS} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, system)
		}
	}
	if len(blocks) < 2 || blocks[0].Cache != true {
		t.Fatalf("environment must follow the cached base: %+v", blocks)
	}
	if blocks[1].Cache {
		t.Fatalf("environment block must stay uncached: %+v", blocks[1])
	}
}

func TestEnvironmentSectionOmittedWithoutCWD(t *testing.T) {
	if got := environmentSection("", time.Now()); got != "" {
		t.Fatalf("environmentSection(\"\") = %q, want empty", got)
	}
}

func TestEnvironmentSectionNamesRepositoryRootOnlyWhenDistinct(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "pkg")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := environmentSection(nested, time.Now()); !strings.Contains(got, "Repository root: "+root) {
		t.Fatalf("nested cwd should name the repository root:\n%s", got)
	}
	if got := environmentSection(root, time.Now()); strings.Contains(got, "Repository root:") {
		t.Fatalf("root cwd should not repeat itself:\n%s", got)
	}
}

// blocksContaining counts the system blocks whose text contains want. Tests
// assert on which sections are present rather than on a block count, which
// changes whenever a new dynamic section is added.
func blocksContaining(blocks []llm.SystemBlock, want string) int {
	n := 0
	for _, blk := range blocks {
		if strings.Contains(blk.Text, want) {
			n++
		}
	}
	return n
}

func TestEnvironmentSectionKeepsValuesOnOneLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "odd\nname")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skipf("filesystem rejects newlines in paths: %v", err)
	}
	got := environmentSection(dir, time.Now())
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.HasPrefix(line, "#") && line != "# Environment" {
			t.Fatalf("a path introduced its own heading:\n%s", got)
		}
	}
	if strings.Contains(got, "Working directory: "+dir) {
		t.Fatalf("newline in path was not flattened:\n%s", got)
	}
}

// chatRegistry mirrors what interactive chat registers, so prompt tests see the
// capability sections a chat session would actually get.
func chatRegistry() *tools.Registry {
	return newRegistry("", "", workflow.Tool{}, subagent.AgentTool{})
}
