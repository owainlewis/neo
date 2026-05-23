package main

import (
	"testing"

	"github.com/owainlewis/neo/internal/config"
)

func TestDefinitionForRef_PrefersConfiguredFlowOverFileLookingRef(t *testing.T) {
	cfg := &config.Config{
		Flows: map[string]config.FlowConfig{
			"examples/neo-smoke-flow.yml": {
				Steps:     []string{"write", "review"},
				RetryFrom: "write",
				MaxRounds: 2,
			},
		},
	}

	def, err := definitionForRef(cfg, "examples/neo-smoke-flow.yml")
	if err != nil {
		t.Fatalf("definitionForRef: %v", err)
	}
	if def.Name != "examples/neo-smoke-flow.yml" {
		t.Fatalf("Name = %q", def.Name)
	}
	if len(def.StepDefs) != 0 {
		t.Fatalf("expected named config flow, got file flow defs: %+v", def.StepDefs)
	}
	if len(def.Steps) != 2 || def.Steps[0] != "write" || def.Steps[1] != "review" {
		t.Fatalf("Steps = %v", def.Steps)
	}
	if def.RetryFrom != "write" || def.MaxRounds != 2 {
		t.Fatalf("retry config not preserved: %+v", def)
	}
}
