package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const githubReleasesURL = "https://api.github.com/repos/owainlewis/neo/releases"
const githubReleaseDownloadBaseURL = "https://github.com/owainlewis/neo/releases/download"

var stableVersionRE = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

type githubRelease struct {
	TagName         string `json:"tag_name"`
	TargetCommitish string `json:"target_commitish"`
	CreatedAt       string `json:"created_at"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
}

type updateCheckResult struct {
	Installed string
	Latest    string
	Commit    string
	Available bool
}

type updateInstallResult struct {
	Installed      string
	Latest         string
	Commit         string
	Target         string
	AlreadyCurrent bool
}

type updateOptions struct {
	Check   bool
	Nightly bool
}

type versionParts struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease bool
	Dev        bool
}

func runUpdate(ctx context.Context, args []string) {
	opts, err := parseUpdateOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: neo update [--check] [--nightly]")
		os.Exit(2)
		return
	}
	if opts.Nightly {
		runNightlyUpdate(ctx, opts)
		return
	}
	runStableUpdate(ctx, opts)
}

func parseUpdateOptions(args []string) (updateOptions, error) {
	var opts updateOptions
	for _, arg := range args {
		switch arg {
		case "--check":
			opts.Check = true
		case "--nightly":
			opts.Nightly = true
		default:
			return updateOptions{}, fmt.Errorf("unknown update flag %q", arg)
		}
	}
	return opts, nil
}

func runStableUpdate(ctx context.Context, opts updateOptions) {
	switch {
	case opts.Check:
		result, err := checkStableUpdate(ctx, http.DefaultClient, githubReleasesURL, Version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
			return
		}
		printUpdateCheck("latest stable", result)
	default:
		target, err := currentExecutablePath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
			return
		}
		result, err := installStableUpdate(ctx, http.DefaultClient, githubReleasesURL, githubReleaseDownloadBaseURL, Version, target, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
			return
		}
		printUpdateInstall("latest stable", result)
	}
}

func runNightlyUpdate(ctx context.Context, opts updateOptions) {
	switch {
	case opts.Check:
		result, err := checkNightlyUpdate(ctx, http.DefaultClient, githubReleasesURL, Version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
			return
		}
		printUpdateCheck("latest nightly", result)
	default:
		target, err := currentExecutablePath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
			return
		}
		result, err := installNightlyUpdate(ctx, http.DefaultClient, githubReleasesURL, githubReleaseDownloadBaseURL, Version, target, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
			return
		}
		printUpdateInstall("latest nightly", result)
	}
}

func printUpdateCheck(latestLabel string, result updateCheckResult) {
	fmt.Printf("installed: %s\n", result.Installed)
	fmt.Printf("%s: %s\n", latestLabel, result.Latest)
	if result.Commit != "" {
		fmt.Printf("commit: %s\n", result.Commit)
	}
	if result.Available {
		fmt.Println("update available: yes")
	} else {
		fmt.Println("update available: no")
	}
}

func printUpdateInstall(latestLabel string, result updateInstallResult) {
	fmt.Printf("installed: %s\n", result.Installed)
	fmt.Printf("%s: %s\n", latestLabel, result.Latest)
	if result.Commit != "" {
		fmt.Printf("commit: %s\n", result.Commit)
	}
	if result.AlreadyCurrent {
		fmt.Println("already current")
		return
	}
	fmt.Printf("updated: %s\n", result.Target)
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

func checkNightlyUpdate(ctx context.Context, httpc *http.Client, endpoint, installed string) (updateCheckResult, error) {
	latest, err := latestNightlyRelease(ctx, httpc, endpoint)
	if err != nil {
		return updateCheckResult{}, err
	}
	return updateCheckResult{
		Installed: installed,
		Latest:    latest.TagName,
		Commit:    latest.TargetCommitish,
		Available: nightlyUpdateAvailable(installed, latest.TagName),
	}, nil
}

func installStableUpdate(ctx context.Context, httpc *http.Client, releaseEndpoint, downloadBase, installed, targetPath, goos, goarch string) (updateInstallResult, error) {
	latest, err := latestStableRelease(ctx, httpc, releaseEndpoint)
	if err != nil {
		return updateInstallResult{}, err
	}
	result := updateInstallResult{Installed: installed, Latest: latest, Target: targetPath}
	available, err := updateAvailable(installed, latest)
	if err != nil {
		return updateInstallResult{}, err
	}
	if !available {
		result.AlreadyCurrent = true
		return result, nil
	}
	asset, err := releaseAssetName(goos, goarch)
	if err != nil {
		return updateInstallResult{}, err
	}
	if err := installReleaseAsset(ctx, httpc, downloadBase, latest, asset, targetPath); err != nil {
		return updateInstallResult{}, err
	}
	return result, nil
}

func installNightlyUpdate(ctx context.Context, httpc *http.Client, releaseEndpoint, downloadBase, installed, targetPath, goos, goarch string) (updateInstallResult, error) {
	latest, err := latestNightlyRelease(ctx, httpc, releaseEndpoint)
	if err != nil {
		return updateInstallResult{}, err
	}
	result := updateInstallResult{Installed: installed, Latest: latest.TagName, Commit: latest.TargetCommitish, Target: targetPath}
	if !nightlyUpdateAvailable(installed, latest.TagName) {
		result.AlreadyCurrent = true
		return result, nil
	}
	asset, err := nightlyReleaseAssetName(goos, goarch, latest.TagName)
	if err != nil {
		return updateInstallResult{}, err
	}
	if err := installReleaseAsset(ctx, httpc, downloadBase, latest.TagName, asset, targetPath); err != nil {
		return updateInstallResult{}, err
	}
	return result, nil
}

func latestStableRelease(ctx context.Context, httpc *http.Client, endpoint string) (string, error) {
	releases, err := fetchReleases(ctx, httpc, endpoint)
	if err != nil {
		return "", err
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

func latestNightlyRelease(ctx context.Context, httpc *http.Client, endpoint string) (githubRelease, error) {
	releases, err := fetchReleases(ctx, httpc, endpoint)
	if err != nil {
		return githubRelease{}, err
	}
	var latest githubRelease
	for _, rel := range releases {
		if rel.Draft || !rel.Prerelease || !strings.HasPrefix(rel.TagName, "nightly-") {
			continue
		}
		if latest.TagName == "" || rel.CreatedAt > latest.CreatedAt || (rel.CreatedAt == latest.CreatedAt && rel.TagName > latest.TagName) {
			latest = rel
		}
	}
	if latest.TagName == "" {
		return githubRelease{}, fmt.Errorf("no nightly releases found")
	}
	return latest, nil
}

func fetchReleases(ctx context.Context, httpc *http.Client, endpoint string) ([]githubRelease, error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "neo-update-check")
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch releases: GitHub returned %s", resp.Status)
	}
	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}
	return releases, nil
}

func currentExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

func releaseAssetName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux":
	default:
		return "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
	}
	return fmt.Sprintf("neo_%s_%s.tar.gz", goos, goarch), nil
}

func nightlyReleaseAssetName(goos, goarch, version string) (string, error) {
	if _, err := releaseAssetName(goos, goarch); err != nil {
		return "", err
	}
	return fmt.Sprintf("neo_%s_%s_%s.tar.gz", goos, goarch, version), nil
}

func installReleaseAsset(ctx context.Context, httpc *http.Client, downloadBase, tag, asset, targetPath string) error {
	archive, err := downloadReleaseAsset(ctx, httpc, downloadBase, tag, asset)
	if err != nil {
		return err
	}
	checksums, err := downloadReleaseAsset(ctx, httpc, downloadBase, tag, "checksums.txt")
	if err != nil {
		return err
	}
	if err := verifyAssetChecksum(asset, archive, checksums); err != nil {
		return err
	}
	binary, err := extractBinaryFromTarGz(archive, "neo")
	if err != nil {
		return err
	}
	return replaceExecutable(targetPath, binary)
}

func downloadReleaseAsset(ctx context.Context, httpc *http.Client, baseURL, tag, asset string) ([]byte, error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	url := strings.TrimRight(baseURL, "/") + "/" + tag + "/" + asset
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "neo-update")
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s: server returned %s", asset, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	return body, nil
}

func verifyAssetChecksum(asset string, body, checksums []byte) error {
	want := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == asset {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("checksum for %s not found", asset)
	}
	sum := sha256.Sum256(body)
	got := fmt.Sprintf("%x", sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return nil
}

func extractBinaryFromTarGz(archive []byte, binaryName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != binaryName {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s from archive: %w", binaryName, err)
		}
		return body, nil
	}
	return nil, fmt.Errorf("%s not found in archive", binaryName)
}

func replaceExecutable(targetPath string, body []byte) error {
	info, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("inspect current binary: %w", err)
	}
	mode := info.Mode().Perm()
	dir := filepath.Dir(targetPath)
	tmp, err := os.CreateTemp(dir, ".neo-update-*")
	if err != nil {
		return fmt.Errorf("create update file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write update file: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("prepare update file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close update file: %w", err)
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("replace current binary: %w", err)
	}
	cleanup = false
	return nil
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

func nightlyUpdateAvailable(installed, latest string) bool {
	installed = strings.TrimSpace(installed)
	if installed == "" || installed == "dev" {
		return true
	}
	installed = strings.TrimSuffix(installed, "-dirty")
	return installed != latest
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
