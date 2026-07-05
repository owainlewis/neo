package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const githubReleasesURL = "https://api.github.com/repos/owainlewis/neo/releases"

var stableVersionRE = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type updateCheckResult struct {
	Installed string
	Latest    string
	Available bool
}

type versionParts struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease bool
	Dev        bool
}

func runUpdate(ctx context.Context, args []string) {
	if len(args) != 1 || args[0] != "--check" {
		fmt.Fprintln(os.Stderr, "usage: neo update --check")
		os.Exit(2)
		return
	}
	result, err := checkStableUpdate(ctx, http.DefaultClient, githubReleasesURL, Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		os.Exit(1)
		return
	}
	fmt.Printf("installed: %s\n", result.Installed)
	fmt.Printf("latest stable: %s\n", result.Latest)
	if result.Available {
		fmt.Println("update available: yes")
	} else {
		fmt.Println("update available: no")
	}
}

func checkStableUpdate(ctx context.Context, httpc *http.Client, endpoint, installed string) (updateCheckResult, error) {
	latest, err := latestStableRelease(ctx, httpc, endpoint)
	if err != nil {
		return updateCheckResult{}, err
	}
	available, err := updateAvailable(installed, latest)
	if err != nil {
		return updateCheckResult{}, err
	}
	return updateCheckResult{Installed: installed, Latest: latest, Available: available}, nil
}

func latestStableRelease(ctx context.Context, httpc *http.Client, endpoint string) (string, error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "neo-update-check")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch releases: GitHub returned %s", resp.Status)
	}
	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("parse releases: %w", err)
	}
	var latest string
	for _, rel := range releases {
		if rel.Draft || rel.Prerelease || !isStableVersion(rel.TagName) {
			continue
		}
		if latest == "" || compareStableVersions(rel.TagName, latest) > 0 {
			latest = rel.TagName
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no stable v* releases found")
	}
	return latest, nil
}

func updateAvailable(installed, latest string) (bool, error) {
	current, err := parseInstalledVersion(installed)
	if err != nil {
		return false, err
	}
	if current.Dev {
		return true, nil
	}
	target, ok := parseStableVersion(latest)
	if !ok {
		return false, fmt.Errorf("latest stable release %q is not a valid vMAJOR.MINOR.PATCH tag", latest)
	}
	cmp := compareParts(current, target)
	if cmp < 0 {
		return true, nil
	}
	if cmp == 0 && current.Prerelease {
		return true, nil
	}
	return false, nil
}

func parseInstalledVersion(version string) (versionParts, error) {
	version = strings.TrimSpace(version)
	if version == "" || version == "dev" {
		return versionParts{Dev: true}, nil
	}
	version = strings.TrimSuffix(version, "-dirty")
	base := version
	prerelease := false
	if i := strings.IndexAny(base, "-+"); i >= 0 {
		prerelease = strings.HasPrefix(base[i:], "-")
		base = base[:i]
	}
	parts, ok := parseStableVersion(base)
	if !ok {
		return versionParts{}, fmt.Errorf("installed version %q is not comparable to stable releases", version)
	}
	parts.Prerelease = prerelease
	return parts, nil
}

func isStableVersion(version string) bool {
	_, ok := parseStableVersion(version)
	return ok
}

func parseStableVersion(version string) (versionParts, bool) {
	m := stableVersionRE.FindStringSubmatch(version)
	if m == nil {
		return versionParts{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return versionParts{Major: major, Minor: minor, Patch: patch}, true
}

func compareStableVersions(a, b string) int {
	av, _ := parseStableVersion(a)
	bv, _ := parseStableVersion(b)
	return compareParts(av, bv)
}

func compareParts(a, b versionParts) int {
	switch {
	case a.Major != b.Major:
		return cmpInt(a.Major, b.Major)
	case a.Minor != b.Minor:
		return cmpInt(a.Minor, b.Minor)
	default:
		return cmpInt(a.Patch, b.Patch)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
