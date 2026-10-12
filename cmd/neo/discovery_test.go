package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/skills"
)

func TestDiscoveryWarningsKeepValidSkillsAndProfiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	t.Chdir(cwd)
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(cwd, ".neo", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/good/SKILL.md", "good instructions")
	write("skills/bad/SKILL.md", "---\nname: [bad\n---\nbad")
	write("skills/large/SKILL.md", strings.Repeat("x", 32*1024+1))
	write("agents/good.md", "good profile")
	bad := filepath.Join(cwd, ".neo", "agents", "bad.md")
	if err := os.Symlink(filepath.Join(cwd, "missing"), bad); err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	sk := loadSkills(&config.Config{}, cwd, &warnings)
	if _, ok := skills.Find(sk, "good"); !ok {
		t.Fatal("CLI discarded good skill")
	}
	for _, name := range []string{"bad", "large"} {
		if !strings.Contains(warnings.String(), filepath.Join(name, "SKILL.md")) {
			t.Fatalf("warnings = %q", warnings.String())
		}
	}
	warnings.Reset()
	p, ok := loadProfile(cwd, "good", &warnings)
	if !ok || p.Body != "good profile" || !strings.Contains(warnings.String(), "warning: agents:") {
		t.Fatalf("profile = %+v, %v; warnings = %q", p, ok, warnings.String())
	}
	warnings.Reset()
	var out bytes.Buffer
	if code := runAgents(stdio{out: &out, err: &warnings}); code != 0 {
		t.Fatalf("agents exited %d", code)
	}
	if !strings.Contains(out.String(), "good") || !strings.Contains(warnings.String(), bad) {
		t.Fatalf("stdout = %q; stderr = %q", out.String(), warnings.String())
	}
}
