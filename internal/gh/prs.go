package gh

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PRInfo is a minimal open PR used for stack parent resolution.
type PRInfo struct {
	Head   string
	Base   string
	Number int
	URL    string
	Draft  bool
}

// CacheTTL is how long the on-disk PR parent cache is considered fresh.
const CacheTTL = 5 * time.Minute

type prCacheFile struct {
	FetchedAt time.Time         `json:"fetched_at"`
	Parents   map[string]string `json:"parents"`           // head → base
	Numbers   map[string]int    `json:"numbers,omitempty"` // head → PR number
	URLs      map[string]string `json:"urls,omitempty"`    // head → PR url
}

// PRCache is the on-disk / in-memory open-PR snapshot.
type PRCache struct {
	Parents map[string]string
	Numbers map[string]int
	URLs    map[string]string
}

// ListOpenPRParents returns headRefName → baseRefName for open PRs (one gh call).
func (c *Client) ListOpenPRParents() (map[string]string, error) {
	infos, err := c.ListOpenPRs()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(infos))
	for head, info := range infos {
		out[head] = info.Base
	}
	return out, nil
}

// ListOpenPRs returns open PRs keyed by head branch name.
func (c *Client) ListOpenPRs() (map[string]PRInfo, error) {
	if !Available() {
		return nil, fmt.Errorf("gh not available")
	}
	cmd := exec.Command("gh", "pr", "list", "--state", "open", "--limit", "200",
		"--json", "headRefName,baseRefName,number,url,isDraft")
	if c.Dir != "" {
		cmd.Dir = c.Dir
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh pr list: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var rows []struct {
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
		Number      int    `json:"number"`
		URL         string `json:"url"`
		IsDraft     bool   `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("gh pr list parse: %w", err)
	}
	m := make(map[string]PRInfo, len(rows))
	for _, r := range rows {
		head := strings.TrimSpace(r.HeadRefName)
		base := strings.TrimSpace(r.BaseRefName)
		if head == "" || base == "" {
			continue
		}
		// If GitHub returns multiple open PRs for the same head, keep the
		// highest number (newest) so the UI never keys two rows on one branch.
		if prev, ok := m[head]; ok && prev.Number >= r.Number {
			continue
		}
		m[head] = PRInfo{
			Head:   head,
			Base:   base,
			Number: r.Number,
			URL:    r.URL,
			Draft:  r.IsDraft,
		}
	}
	return m, nil
}

// CachePath returns .git/git-stack/pr-parents.json for a git dir.
func CachePath(gitDir string) string {
	return filepath.Join(gitDir, "git-stack", "pr-parents.json")
}

// LoadPRCache reads the on-disk PR cache. ok is false if missing/stale/invalid.
func LoadPRCache(gitDir string, maxAge time.Duration) (PRCache, bool) {
	path := CachePath(gitDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return PRCache{}, false
	}
	var c prCacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		return PRCache{}, false
	}
	if c.Parents == nil {
		return PRCache{}, false
	}
	if maxAge > 0 && time.Since(c.FetchedAt) > maxAge {
		return PRCache{}, false
	}
	if c.Numbers == nil {
		c.Numbers = map[string]int{}
	}
	if c.URLs == nil {
		c.URLs = map[string]string{}
	}
	return PRCache{Parents: c.Parents, Numbers: c.Numbers, URLs: c.URLs}, true
}

// LoadPRParentCache reads parents only (compat helper).
func LoadPRParentCache(gitDir string, maxAge time.Duration) (map[string]string, bool) {
	c, ok := LoadPRCache(gitDir, maxAge)
	if !ok {
		return nil, false
	}
	return c.Parents, true
}

// SavePRCache writes full open-PR snapshot under .git/git-stack/.
func SavePRCache(gitDir string, infos map[string]PRInfo) error {
	dir := filepath.Join(gitDir, "git-stack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	parents := make(map[string]string, len(infos))
	numbers := make(map[string]int, len(infos))
	urls := make(map[string]string, len(infos))
	for head, info := range infos {
		if info.Base != "" {
			parents[head] = info.Base
		}
		if info.Number > 0 {
			numbers[head] = info.Number
		}
		if info.URL != "" {
			urls[head] = info.URL
		}
	}
	c := prCacheFile{
		FetchedAt: time.Now().UTC(),
		Parents:   parents,
		Numbers:   numbers,
		URLs:      urls,
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(CachePath(gitDir), data, 0o644)
}

// SavePRParentCache writes parents only, preserving numbers/urls when present.
func SavePRParentCache(gitDir string, parents map[string]string) error {
	existing, _ := LoadPRCache(gitDir, 0)
	dir := filepath.Join(gitDir, "git-stack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	c := prCacheFile{
		FetchedAt: time.Now().UTC(),
		Parents:   parents,
		Numbers:   existing.Numbers,
		URLs:      existing.URLs,
	}
	if c.Numbers == nil {
		c.Numbers = map[string]int{}
	}
	if c.URLs == nil {
		c.URLs = map[string]string{}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(CachePath(gitDir), data, 0o644)
}

// InvalidatePRParentCache removes the cache file.
func InvalidatePRParentCache(gitDir string) {
	_ = os.Remove(CachePath(gitDir))
}
