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
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type updateCheckResult struct {
	Installed string
	Latest    string
	Available bool
}

type updateInstallResult struct {
	Installed      string
	Latest         string
	Target         string
	AlreadyCurrent bool
}

type versionParts struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease bool
	Dev        bool
}

func runUpdate(ctx context.Context, args []string) {
	switch {
	case len(args) == 1 && args[0] == "--check":
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
	case len(args) == 0:
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
		fmt.Printf("installed: %s\n", result.Installed)
		fmt.Printf("latest stable: %s\n", result.Latest)
		if result.AlreadyCurrent {
			fmt.Println("already current")
			return
		}
		fmt.Printf("updated: %s\n", result.Target)
	default:
		fmt.Fprintln(os.Stderr, "usage: neo update [--check]")
		os.Exit(2)
		return
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
	archive, err := downloadReleaseAsset(ctx, httpc, downloadBase, latest, asset)
	if err != nil {
		return updateInstallResult{}, err
	}
	checksums, err := downloadReleaseAsset(ctx, httpc, downloadBase, latest, "checksums.txt")
	if err != nil {
		return updateInstallResult{}, err
	}
	if err := verifyAssetChecksum(asset, archive, checksums); err != nil {
		return updateInstallResult{}, err
	}
	binary, err := extractBinaryFromTarGz(archive, "neo")
	if err != nil {
		return updateInstallResult{}, err
	}
	if err := replaceExecutable(targetPath, binary); err != nil {
		return updateInstallResult{}, err
	}
	return result, nil
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
