package cli

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	upgradeRepo     = "benwyrosdick/git-stack"
	upgradeBinName  = "git-stack"
	githubAPILatest = "https://api.github.com/repos/" + upgradeRepo + "/releases/latest"
)

// UpgradeOpts controls upgrade behavior.
type UpgradeOpts struct {
	Check bool // only report; do not install
	Force bool // reinstall even when already on latest
}

func cmdUpgrade(args []string) error {
	opts := UpgradeOpts{}
	for _, a := range args {
		switch a {
		case "--check":
			opts.Check = true
		case "--force":
			opts.Force = true
		case "-h", "--help":
			fmt.Fprint(os.Stdout, `usage: git-stack upgrade [--check] [--force]

  Check GitHub Releases for a newer version and install it in place.

  --check   Report whether an update is available; do not install
  --force   Reinstall the latest release even if already up to date
`)
			return nil
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("upgrade: unknown flag %s", a)
			}
			return fmt.Errorf("upgrade: unexpected argument %s", a)
		}
	}
	return Upgrade(opts)
}

// Upgrade checks for a newer release and installs it over the running binary.
func Upgrade(opts UpgradeOpts) error {
	current := ResolvedVersion()
	fmt.Fprintf(os.Stderr, "git-stack upgrade: current version %s\n", current)

	rel, err := fetchLatestRelease()
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	fmt.Fprintf(os.Stderr, "git-stack upgrade: latest release %s\n", rel.TagName)

	newer := versionIsNewer(latest, current)
	if opts.Check {
		if newer {
			fmt.Printf("update available: %s → %s\n", current, latest)
		} else {
			fmt.Printf("up to date: %s\n", current)
		}
		return nil
	}
	if !opts.Force && !newer {
		fmt.Fprintf(os.Stderr, "git-stack upgrade: already up to date\n")
		return nil
	}

	dest, err := installTargetPath()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "git-stack upgrade: installing %s → %s\n", rel.TagName, dest)

	if err := installRelease(rel, dest); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "git-stack upgrade: installed %s\n", latest)
	// Print the new binary's version if possible.
	if out, err := runVersion(dest); err == nil && out != "" {
		fmt.Println(out)
	} else {
		fmt.Println(latest)
	}
	return nil
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease() (*ghRelease, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, githubAPILatest, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "git-stack/"+ResolvedVersion())

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upgrade: fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("upgrade: GitHub API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("upgrade: decode release: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("upgrade: no releases found for %s", upgradeRepo)
	}
	return &rel, nil
}

func installTargetPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("upgrade: resolve executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("upgrade: resolve executable: %w", err)
	}
	return exe, nil
}

func installRelease(rel *ghRelease, dest string) error {
	osName, arch, err := releasePlatform()
	if err != nil {
		return err
	}
	ver := strings.TrimPrefix(rel.TagName, "v")
	// GoReleaser: git-stack_{Version}_{Os}_{Arch}.tar.gz (version without leading v)
	candidates := []string{
		fmt.Sprintf("%s_%s_%s_%s.tar.gz", upgradeBinName, ver, osName, arch),
		fmt.Sprintf("%s_%s_%s_%s.tar.gz", upgradeBinName, rel.TagName, osName, arch),
		fmt.Sprintf("%s_%s_%s.tar.gz", upgradeBinName, osName, arch),
	}

	var asset *ghAsset
	var assetName string
	for _, name := range candidates {
		if a := findAsset(rel.Assets, name); a != nil {
			asset = a
			assetName = name
			break
		}
	}
	if asset == nil {
		return fmt.Errorf("upgrade: no release asset for %s/%s in %s\n  tried: %s",
			osName, arch, rel.TagName, strings.Join(candidates, ", "))
	}

	tmpDir, err := os.MkdirTemp("", "git-stack-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	fmt.Fprintf(os.Stderr, "git-stack upgrade: downloading %s\n", asset.BrowserDownloadURL)
	if err := downloadFile(asset.BrowserDownloadURL, archivePath); err != nil {
		return err
	}

	if sumsURL := findAsset(rel.Assets, "checksums.txt"); sumsURL != nil {
		sumsPath := filepath.Join(tmpDir, "checksums.txt")
		if err := downloadFile(sumsURL.BrowserDownloadURL, sumsPath); err == nil {
			if err := verifyChecksum(archivePath, assetName, sumsPath); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "git-stack upgrade: checksum ok\n")
		} else {
			fmt.Fprintf(os.Stderr, "git-stack upgrade: could not download checksums; skipping verify\n")
		}
	}

	binPath, err := extractBinary(archivePath, tmpDir)
	if err != nil {
		return err
	}
	return replaceExecutable(binPath, dest)
}

func releasePlatform() (osName, arch string, err error) {
	switch runtime.GOOS {
	case "linux", "darwin":
		osName = runtime.GOOS
	default:
		return "", "", fmt.Errorf("upgrade: unsupported OS %s (use install script or go install)", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
		arch = runtime.GOARCH
	default:
		return "", "", fmt.Errorf("upgrade: unsupported architecture %s", runtime.GOARCH)
	}
	return osName, arch, nil
}

func findAsset(assets []ghAsset, name string) *ghAsset {
	for i := range assets {
		if assets[i].Name == name {
			return &assets[i]
		}
	}
	return nil
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "git-stack/"+ResolvedVersion())
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upgrade: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upgrade: download %s: %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("upgrade: download: %w", err)
	}
	return nil
}

func verifyChecksum(archivePath, assetName, sumsPath string) error {
	data, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	var expect string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// format: "<sha256>  <filename>" or "<sha256> *<filename>"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName {
			expect = fields[0]
			break
		}
	}
	if expect == "" {
		fmt.Fprintf(os.Stderr, "git-stack upgrade: checksum line for %s not found; skipping verify\n", assetName)
		return nil
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expect) {
		return fmt.Errorf("upgrade: checksum verification failed for %s", assetName)
	}
	return nil
}

func extractBinary(archivePath, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("upgrade: open archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var binPath string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("upgrade: read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base != upgradeBinName {
			continue
		}
		out := filepath.Join(destDir, upgradeBinName)
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(w, tr); err != nil {
			w.Close()
			return "", err
		}
		if err := w.Close(); err != nil {
			return "", err
		}
		binPath = out
		// keep scanning in case of nested paths; last match wins, first is fine
		break
	}
	if binPath == "" {
		return "", fmt.Errorf("upgrade: binary %s not found in archive", upgradeBinName)
	}
	return binPath, nil
}

func replaceExecutable(src, dest string) error {
	// Write to a temp file in the same directory so rename is atomic on the same fs.
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, upgradeBinName+".new-*")
	if err != nil {
		return fmt.Errorf("upgrade: cannot write to %s: %w\n  (try installing to a writable dir, e.g. ~/.local/bin)", dir, err)
	}
	tmpPath := tmp.Name()
	// Ensure cleanup on failure.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	in, err := os.Open(src)
	if err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		in.Close()
		tmp.Close()
		return err
	}
	in.Close()
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Prefer rename over dest (works for replacing a running binary on Unix).
	if err := os.Rename(tmpPath, dest); err != nil {
		// Fallback: remove dest then rename (some filesystems / permissions).
		if rmErr := os.Remove(dest); rmErr != nil {
			return fmt.Errorf("upgrade: replace %s: %w", dest, err)
		}
		if err2 := os.Rename(tmpPath, dest); err2 != nil {
			return fmt.Errorf("upgrade: replace %s: %w", dest, err2)
		}
	}
	success = true
	return nil
}

func runVersion(bin string) (string, error) {
	// Avoid importing os/exec at call sites that don't need it — local import is fine.
	// Use a short helper via exec.
	return runCmdVersion(bin)
}

// versionIsNewer reports whether latest is a newer release than current.
// Non-release current builds (dev, dev+rev, empty) are always considered older.
func versionIsNewer(latest, current string) bool {
	latest = normalizeVersion(latest)
	current = normalizeVersion(current)
	if latest == "" {
		return false
	}
	if !isReleaseVersion(current) {
		return true
	}
	if latest == current {
		return false
	}
	return compareSemver(latest, current) > 0
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	// Strip build metadata (+dirty etc.) and keep pre-release for identity,
	// but compareSemver will use numeric core only.
	return v
}

func isReleaseVersion(v string) bool {
	v = normalizeVersion(v)
	if v == "" || v == "dev" || strings.HasPrefix(v, "dev+") || strings.HasPrefix(v, "dev-") {
		return false
	}
	// go install pseudo-versions and plain semver start with a digit
	return v[0] >= '0' && v[0] <= '9'
}

// compareSemver compares two version strings (major.minor.patch[...]).
// Returns 1 if a>b, -1 if a<b, 0 if equal on the numeric core.
func compareSemver(a, b string) int {
	ap := semverParts(a)
	bp := semverParts(b)
	n := len(ap)
	if len(bp) > n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(ap) {
			ai = ap[i]
		}
		if i < len(bp) {
			bi = bp[i]
		}
		if ai > bi {
			return 1
		}
		if ai < bi {
			return -1
		}
	}
	return 0
}

func semverParts(v string) []int {
	v = normalizeVersion(v)
	// Drop pre-release / build: 1.2.3-rc.1+meta → 1.2.3
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var parts []int
	for _, p := range strings.Split(v, ".") {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		parts = append(parts, n)
	}
	return parts
}
