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
	Parents   map[string]string `json:"parents"` // head → base
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
		if r.HeadRefName == "" || r.BaseRefName == "" {
			continue
		}
		m[r.HeadRefName] = PRInfo{
			Head:   r.HeadRefName,
			Base:   r.BaseRefName,
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

// LoadPRParentCache reads the on-disk cache. ok is false if missing/stale/invalid.
func LoadPRParentCache(gitDir string, maxAge time.Duration) (map[string]string, bool) {
	path := CachePath(gitDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c prCacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, false
	}
	if c.Parents == nil {
		return nil, false
	}
	if maxAge > 0 && time.Since(c.FetchedAt) > maxAge {
		return nil, false
	}
	return c.Parents, true
}

// SavePRParentCache writes the parent map under .git/git-stack/.
func SavePRParentCache(gitDir string, parents map[string]string) error {
	dir := filepath.Join(gitDir, "git-stack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	c := prCacheFile{FetchedAt: time.Now().UTC(), Parents: parents}
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
