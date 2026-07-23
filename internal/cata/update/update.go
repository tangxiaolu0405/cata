// Package update downloads GitHub Releases and replaces the installed binaries.
package update

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cata/internal/cata/version"
)

const defaultRepo = "tangxiaolu0405/cata"

// Options controls cata update behavior.
type Options struct {
	CheckOnly bool
	Force     bool
	Repo      string // owner/name; empty → CATA_REPO or defaultRepo
	Stdout    io.Writer
	Stderr    io.Writer
}

// Run checks for a newer release and optionally installs it beside the running binary.
func Run(opts Options) error {
	out := opts.Stdout
	if out == nil {
		out = os.Stdout
	}
	errOut := opts.Stderr
	if errOut == nil {
		errOut = os.Stderr
	}

	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = strings.TrimSpace(os.Getenv("CATA_REPO"))
	}
	if repo == "" {
		repo = defaultRepo
	}

	artifact, archiveExt, binName, gatewayName, err := detectArtifact()
	if err != nil {
		return err
	}
	archiveName := artifact + "." + archiveExt

	current := version.Version
	fmt.Fprintf(out, "current: %s\n", current)

	rel, err := fetchLatestRelease(repo)
	if err != nil {
		return err
	}
	latest := rel.TagName
	fmt.Fprintf(out, "latest:  %s\n", latest)

	if !opts.Force && !NeedsUpdate(current, latest) {
		fmt.Fprintln(out, "already up to date")
		return nil
	}
	if opts.CheckOnly {
		fmt.Fprintf(out, "update available: %s → %s\n", current, latest)
		return nil
	}

	url := assetURL(rel, archiveName)
	if url == "" {
		url = fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, latest, archiveName)
	}
	fmt.Fprintf(out, "download: %s\n", url)

	tmpDir, err := os.MkdirTemp("", "cata-update-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, archiveName)
	if err := downloadFile(url, archivePath); err != nil {
		return err
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return err
	}
	if err := extractArchive(archivePath, archiveExt, extractDir); err != nil {
		return err
	}

	newBin := filepath.Join(extractDir, binName)
	newGateway := filepath.Join(extractDir, gatewayName)
	if _, err := os.Stat(newBin); err != nil {
		return fmt.Errorf("archive missing %s", binName)
	}
	if _, err := os.Stat(newGateway); err != nil {
		return fmt.Errorf("archive missing %s", gatewayName)
	}

	installDir, err := installDir()
	if err != nil {
		return err
	}
	dstBin := filepath.Join(installDir, binName)
	dstGateway := filepath.Join(installDir, gatewayName)

	fmt.Fprintf(out, "install: %s\n", dstBin)
	if err := replaceFile(newBin, dstBin); err != nil {
		return fmt.Errorf("replace %s: %w", binName, err)
	}
	if err := adhocResign(dstBin); err != nil {
		return fmt.Errorf("resign %s: %w", binName, err)
	}
	fmt.Fprintf(out, "install: %s\n", dstGateway)
	if err := replaceFile(newGateway, dstGateway); err != nil {
		return fmt.Errorf("replace %s: %w", gatewayName, err)
	}
	if err := adhocResign(dstGateway); err != nil {
		return fmt.Errorf("resign %s: %w", gatewayName, err)
	}

	fmt.Fprintf(out, "updated: %s → %s\n", current, latest)
	fmt.Fprintln(errOut, "note: restart any running cata/cata-gateway processes to use the new binaries")
	return nil
}

// NeedsUpdate reports whether current should be replaced by latest.
func NeedsUpdate(current, latest string) bool {
	c := NormalizeVersion(current)
	l := NormalizeVersion(latest)
	if l == "" {
		return false
	}
	if c == "" || c == "dev" || strings.HasPrefix(c, "dev-") {
		return true
	}
	return c != l
}

// NormalizeVersion strips a leading v and whitespace.
func NormalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

func installDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe), nil
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cata-update")
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("download write: %w", err)
	}
	return nil
}
