package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	Model      string
	PhasesDir  string
	FlowsDir   string
	ArtifactsDir string
}

func Default() Config {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".neo")
	return Config{
		Model:        envOr("NEO_MODEL", "claude-sonnet-4-6"),
		PhasesDir:    envOr("NEO_PHASES_DIR", findFirstDir("phases", filepath.Join(root, "phases"))),
		FlowsDir:     envOr("NEO_FLOWS_DIR", findFirstDir("flows", filepath.Join(root, "flows"))),
		ArtifactsDir: envOr("NEO_ARTIFACTS_DIR", ".agent/runs"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func findFirstDir(candidates ...string) string {
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return candidates[len(candidates)-1]
}
