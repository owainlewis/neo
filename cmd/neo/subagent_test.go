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

// The prompt must describe a tool only where that tool exists. A registry
// carrying neither capability tool gets neither section, so instructing it to
// use them would be a contract the model cannot keep.
func TestChatSystemScopesCapabilityGuidanceToRegisteredTools(t *testing.T) {
	cfg := &config.Config{}
	capabilityPhrases := []string{"workflow tool", "agent tool", "subagent"}

	base, _ := chatSystem(cfg, t.TempDir(), nil, profile.Profile{}, newRegistry("", ""), io.Discard)
	for _, phrase := range capabilityPhrases {
		if strings.Contains(base, phrase) {
			t.Fatalf("prompt names %q but has no such tool:\n%s", phrase, base)
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
		{"base", newRegistry("", "")},
		{"headless", headlessTestRegistry(t)},
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

// headlessTestRegistry mirrors what `neo run` registers.
func headlessTestRegistry(t *testing.T) *tools.Registry {
	t.Helper()
	reg, _ := headlessRegistry(context.Background(), &config.Config{},
		&llmtest.FakeProvider{}, "main-model", t.TempDir(), t.TempDir())
	return reg
}

// chatRegistry mirrors what interactive chat registers, so prompt tests see the
// capability sections a chat session would actually get.
func chatRegistry() *tools.Registry {
	return newRegistry("", "", workflow.Tool{}, subagent.AgentTool{})
}

// headlessRegistry is what `neo run` actually builds, so headless tests must
// assert against it rather than the bare base registry.
func TestHeadlessRegistryExposesAgentToolWithoutWorkflow(t *testing.T) {
	reg, runner := headlessRegistry(context.Background(), &config.Config{},
		&llmtest.FakeProvider{}, "main-model", t.TempDir(), t.TempDir())
	if runner == nil {
		t.Fatal("headless registry did not build a subagent runner")
	}
	for _, want := range []string{"agent", "bash", "grep", "glob", "read_file", "write_file", "edit_file"} {
		if _, ok := reg.Get(want); !ok {
			t.Fatalf("headless registry is missing %q, has %v", want, reg.Names())
		}
	}
	if _, ok := reg.Get("workflow"); ok {
		t.Fatalf("headless registry must not register the workflow tool: %v", reg.Names())
	}
}

// os.Getwd can fail. A subagent with no directory has nowhere to run, so
// headless falls back to the base registry rather than spawning children.
func TestHeadlessRegistryWithoutWorkingDirectoryHasNoAgentTool(t *testing.T) {
	reg, runner := headlessRegistry(context.Background(), &config.Config{},
		&llmtest.FakeProvider{}, "main-model", "", t.TempDir())
	if runner != nil {
		t.Fatal("no working directory should mean no subagent runner")
	}
	if _, ok := reg.Get("agent"); ok {
		t.Fatalf("headless registry spawned an agent tool with no cwd: %v", reg.Names())
	}
}

func TestHeadlessSubagentBackendFollowsCoordinatorByDefault(t *testing.T) {
	coordinator := &llmtest.FakeProvider{}
	_, runner := headlessRegistry(context.Background(), &config.Config{},
		coordinator, "main-model", t.TempDir(), t.TempDir())
	if runner.Provider != coordinator || runner.DefaultModel != "main-model" {
		t.Fatalf("worker backend = %T/%q, want the coordinator's", runner.Provider, runner.DefaultModel)
	}
}

func TestHeadlessSubagentBackendUsesConfiguredSubagentsBlock(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	cfg := &config.Config{Subagents: config.Backend{Provider: "anthropic", Model: "worker-model"}}
	_, runner := headlessRegistry(context.Background(), cfg,
		&llmtest.FakeProvider{}, "main-model", t.TempDir(), t.TempDir())
	if runner.Provider.Name() != "anthropic" || runner.DefaultModel != "worker-model" {
		t.Fatalf("worker backend = %s/%q, want anthropic/worker-model", runner.Provider.Name(), runner.DefaultModel)
	}
}

// The headless coordinator can delegate, and the child sees no agent tool, so
// a subagent cannot delegate again.
func TestHeadlessAgentToolDelegatesWithoutNestedDelegation(t *testing.T) {
	worker := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("child report")}}
	reg, _ := headlessRegistry(context.Background(), &config.Config{},
		worker, "main-model", t.TempDir(), t.TempDir())
	at, ok := reg.Get("agent")
	if !ok {
		t.Fatal("headless registry has no agent tool")
	}
	out, err := at.Run(context.Background(), map[string]any{"prompt": "summarize the repo"})
	if err != nil {
		t.Fatalf("agent tool run: %v", err)
	}
	if !strings.Contains(out, `"ok":true`) || !strings.Contains(out, "child report") {
		t.Fatalf("agent tool output = %q", out)
	}
	if len(worker.Calls) == 0 {
		t.Fatal("the worker backend was never called")
	}
	for _, spec := range worker.Calls[0].Tools {
		if spec.Name == "agent" {
			t.Fatal("a headless subagent was given the agent tool and could delegate again")
		}
	}
}

// inspect children are read-only and parallel-safe; work children are neither.
func TestHeadlessAgentToolInspectModeIsReadOnlyAndParallelSafe(t *testing.T) {
	worker := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("inspection")}}
	reg, _ := headlessRegistry(context.Background(), &config.Config{},
		worker, "main-model", t.TempDir(), t.TempDir())
	at, _ := reg.Get("agent")
	parallel, ok := at.(interface{ ParallelSafe(map[string]any) bool })
	if !ok {
		t.Fatal("headless agent tool does not report parallel safety")
	}
	if !parallel.ParallelSafe(map[string]any{"prompt": "look", "mode": "inspect"}) {
		t.Fatal("inspect input should be parallel-safe")
	}
	for _, input := range []map[string]any{
		{"prompt": "change", "mode": "work"},
		{"prompt": "change"},
		{"prompt": "", "mode": "inspect"},
	} {
		if parallel.ParallelSafe(input) {
			t.Fatalf("input %v should not be parallel-safe", input)
		}
	}

	if _, err := at.Run(context.Background(), map[string]any{"prompt": "look", "mode": "inspect"}); err != nil {
		t.Fatalf("inspect run: %v", err)
	}
	var names []string
	for _, spec := range worker.Calls[0].Tools {
		names = append(names, spec.Name)
	}
	want := map[string]bool{"read_file": true, "grep": true, "glob": true}
	if len(names) != len(want) {
		t.Fatalf("inspect child tools = %v, want exactly %v", names, want)
	}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("inspect child got writable tool %q (%v)", name, names)
		}
	}
}

func TestHeadlessAgentToolReportsCancellationAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func(t *testing.T) context.Context
		code string
	}{
		{"canceled", func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, `"code":"canceled"`},
		{"timeout", func(t *testing.T) context.Context {
			ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
			t.Cleanup(cancel)
			time.Sleep(time.Millisecond)
			return ctx
		}, `"code":"timeout"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, _ := headlessRegistry(context.Background(), &config.Config{},
				&llmtest.FakeProvider{}, "main-model", t.TempDir(), t.TempDir())
			at, _ := reg.Get("agent")
			out, err := at.Run(tc.ctx(t), map[string]any{"prompt": "work"})
			if err != nil {
				t.Fatalf("agent tool run: %v", err)
			}
			if !strings.Contains(out, `"ok":false`) || !strings.Contains(out, tc.code) {
				t.Fatalf("output = %q, want ok:false with %s", out, tc.code)
			}
		})
	}
}

// DefaultBudget's session agent cap still bounds headless delegation.
func TestHeadlessAgentToolKeepsDefaultSupervisorBudgets(t *testing.T) {
	budget := subagent.DefaultBudget()
	if budget.MaxAgents != 20 || budget.MaxWall != 15*time.Minute {
		t.Fatalf("default budget = %+v, want 20 agents / 15m wall", budget)
	}
	responses := make([]llm.Response, budget.MaxAgents)
	for i := range responses {
		responses[i] = llmtest.Text("done")
	}
	reg, _ := headlessRegistry(context.Background(), &config.Config{},
		&llmtest.FakeProvider{Responses: responses}, "main-model", t.TempDir(), t.TempDir())
	at, _ := reg.Get("agent")
	for i := 0; i < budget.MaxAgents; i++ {
		out, err := at.Run(context.Background(), map[string]any{"prompt": "work"})
		if err != nil || !strings.Contains(out, `"ok":true`) {
			t.Fatalf("run %d: out=%q err=%v", i+1, out, err)
		}
	}
	out, err := at.Run(context.Background(), map[string]any{"prompt": "one too many"})
	if err != nil {
		t.Fatalf("agent tool run: %v", err)
	}
	if !strings.Contains(out, `"ok":false`) || !strings.Contains(out, `"code":"admission_denied"`) {
		t.Fatalf("output = %q, want admission_denied", out)
	}
}

// The subagent capability section is gated on the agent tool, so headless now
// gets it and the workflow section stays out.
func TestHeadlessSystemPromptDescribesDelegationOnly(t *testing.T) {
	reg, _ := headlessRegistry(context.Background(), &config.Config{},
		&llmtest.FakeProvider{}, "main-model", t.TempDir(), t.TempDir())
	system, _ := chatSystem(&config.Config{}, t.TempDir(), nil, profile.Profile{}, reg, io.Discard)
	if !strings.Contains(system, "agent tool") || !strings.Contains(system, "# Delegation") {
		t.Fatalf("headless prompt is missing the delegation section:\n%s", system)
	}
	if strings.Contains(system, "workflow tool") || strings.Contains(system, "# Workflow checklist") {
		t.Fatalf("headless prompt describes the workflow tool it does not have:\n%s", system)
	}
}
