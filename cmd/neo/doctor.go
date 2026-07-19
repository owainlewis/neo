package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/owainlewis/neo/internal/auth"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/session"
)

type doctorStatus string

const (
	doctorPass doctorStatus = "pass"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"
)

type doctorCheck struct {
	Status doctorStatus
	Name   string
	Detail string
}

func runDoctor(ctx context.Context) int {
	_ = ctx
	checks := doctorChecks()
	printDoctorChecks(checks)
	for _, check := range checks {
		if check.Status == doctorFail {
			return 1
		}
	}
	return 0
}

func doctorChecks() []doctorCheck {
	var checks []doctorCheck
	cfg, err := config.Load()
	if err != nil {
		// Config-dependent checks are skipped, but the session store and git
		// diagnostics still run so a broken config doesn't hide other problems.
		checks = append(checks,
			doctorCheck{Status: doctorFail, Name: "config", Detail: err.Error()},
			doctorCheck{Status: doctorWarn, Name: "provider", Detail: "skipped: config failed to load"},
			doctorCheck{Status: doctorWarn, Name: "credentials", Detail: "skipped: config failed to load"},
			doctorCheck{Status: doctorWarn, Name: "model", Detail: "skipped: config failed to load"},
		)
	} else {
		checks = append(checks, doctorCheck{Status: doctorPass, Name: "config", Detail: "loaded " + cfg.Source()})
		checks = append(checks, doctorProviderCheck(cfg))
		checks = append(checks, doctorCredentialCheck(cfg))
		checks = append(checks, doctorModelCheck(cfg))
	}
	checks = append(checks, doctorSessionStoreCheck())
	checks = append(checks, doctorGitChecks()...)
	return checks
}

func doctorProviderCheck(cfg *config.Config) doctorCheck {
	switch cfg.Provider {
	case "anthropic", "openai", "openrouter", "google":
		return doctorCheck{Status: doctorPass, Name: "provider", Detail: cfg.Provider}
	default:
		return doctorCheck{Status: doctorFail, Name: "provider", Detail: fmt.Sprintf("unknown provider %q", cfg.Provider)}
	}
}

func doctorCredentialCheck(cfg *config.Config) doctorCheck {
	switch cfg.Provider {
	case "anthropic":
		return envCredentialCheck("ANTHROPIC_API_KEY")
	case "openrouter":
		return envCredentialCheck("OPENROUTER_API_KEY")
	case "google":
		return envCredentialCheck("GOOGLE_API_KEY")
	case "openai":
		if cfg.SubscriptionAuth() {
			store, err := auth.DefaultStore()
			if err != nil {
				return doctorCheck{Status: doctorFail, Name: "credentials", Detail: err.Error()}
			}
			if _, ok, err := store.Get(auth.ProviderOpenAICodex); err != nil {
				return doctorCheck{Status: doctorFail, Name: "credentials", Detail: "could not read OpenAI subscription credentials"}
			} else if !ok {
				return doctorCheck{Status: doctorFail, Name: "credentials", Detail: "run `neo login` for OpenAI subscription auth"}
			}
			return doctorCheck{Status: doctorPass, Name: "credentials", Detail: "OpenAI subscription credentials are present"}
		}
		return envCredentialCheck("OPENAI_API_KEY")
	default:
		return doctorCheck{Status: doctorFail, Name: "credentials", Detail: "provider is invalid"}
	}
}

func envCredentialCheck(name string) doctorCheck {
	if strings.TrimSpace(os.Getenv(name)) == "" {
		return doctorCheck{Status: doctorFail, Name: "credentials", Detail: "set " + name}
	}
	return doctorCheck{Status: doctorPass, Name: "credentials", Detail: name + " is set"}
}

func doctorModelCheck(cfg *config.Config) doctorCheck {
	if strings.TrimSpace(cfg.Model) == "" {
		return doctorCheck{Status: doctorFail, Name: "model", Detail: "model is empty"}
	}
	return doctorCheck{Status: doctorPass, Name: "model", Detail: cfg.Model}
}

func doctorSessionStoreCheck() doctorCheck {
	store, err := session.DefaultStore()
	if err != nil {
		return doctorCheck{Status: doctorFail, Name: "sessions", Detail: err.Error()}
	}
	dir := store.Dir()
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return doctorCheck{Status: doctorFail, Name: "sessions", Detail: dir + " is not a directory"}
		}
		return doctorCheck{Status: doctorPass, Name: "sessions", Detail: "store is available at " + shortPath(dir)}
	}
	if os.IsNotExist(err) {
		parent := filepath.Dir(dir)
		if _, parentErr := os.Stat(parent); parentErr != nil {
			return doctorCheck{Status: doctorWarn, Name: "sessions", Detail: "store will be created at " + shortPath(dir)}
		}
		return doctorCheck{Status: doctorWarn, Name: "sessions", Detail: "store does not exist yet at " + shortPath(dir)}
	}
	return doctorCheck{Status: doctorFail, Name: "sessions", Detail: err.Error()}
}

func doctorGitChecks() []doctorCheck {
	checks := make([]doctorCheck, 0, 2)
	if _, err := exec.LookPath("git"); err != nil {
		return []doctorCheck{
			{Status: doctorFail, Name: "git", Detail: "git executable not found in PATH"},
			{Status: doctorWarn, Name: "workspace", Detail: "git workspace check skipped"},
		}
	}
	checks = append(checks, doctorCheck{Status: doctorPass, Name: "git", Detail: "git executable found"})
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		checks = append(checks, doctorCheck{Status: doctorWarn, Name: "workspace", Detail: "current directory is not a git workspace"})
		return checks
	}
	checks = append(checks, doctorCheck{Status: doctorPass, Name: "workspace", Detail: "git root " + shortPath(strings.TrimSpace(string(out)))})
	return checks
}

func printDoctorChecks(checks []doctorCheck) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tCHECK\tDETAIL")
	for _, check := range checks {
		fmt.Fprintf(w, "%s\t%s\t%s\n", check.Status, check.Name, check.Detail)
	}
	_ = w.Flush()
}
