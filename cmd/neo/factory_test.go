package main

import (
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/factory"
)

func TestStepsSectionAdvertisesSteps(t *testing.T) {
	// Even with no project steps, the embedded defaults are advertised.
	s := stepsSection(factory.Resolver{})
	for _, want := range []string{"run_step", "`worker`", "`verify`", "`orchestrator`", "`triage`"} {
		if !strings.Contains(s, want) {
			t.Errorf("steps section missing %q", want)
		}
	}
	// Descriptions ride along.
	if !strings.Contains(s, "end to end") {
		t.Errorf("worker description missing:\n%s", s)
	}
}

func TestChatSystemIncludesStepsSection(t *testing.T) {
	cwd := t.TempDir()
	section := stepsSection(factory.Resolver{})
	system, blocks := chatSystem(&config.Config{}, cwd, nil, section)
	if !strings.Contains(system, "# Workflow steps") {
		t.Fatal("flattened system prompt missing steps section")
	}
	last := blocks[len(blocks)-1]
	if !strings.Contains(last.Text, "# Workflow steps") || last.Cache {
		t.Fatalf("steps section should be the last, uncached block: %+v", last.Cache)
	}
}
