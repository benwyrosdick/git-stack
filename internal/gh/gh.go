// Package gh wraps the GitHub CLI for stacked PR create/retarget.
package gh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/benwyrosdick/git-stack/internal/git"
)

// Client shells out to gh.
type Client struct {
	Dir string
}

func (c *Client) run(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	if c.Dir != "" {
		cmd.Dir = c.Dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Available reports whether gh is on PATH.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// PROpts for create/retarget.
type PROpts struct {
	Branch string
	Base   string // required stack parent (caller resolves)
	Draft  bool
	// StackBranches is the stack ordered base → tip (e.g. trunk, …, branch, …kids).
	// When non-empty, a linked stack section is written at the top of the PR body.
	StackBranches []string
}

// EnsurePR creates or retargets a PR with the given base.
// When StackBranches is set, upserts a stack section (with markers) at the top of the PR body.
func EnsurePR(repo *git.Repo, opts PROpts) (string, error) {
	if !Available() {
		return "", fmt.Errorf("gh is required for git-stack pr")
	}
	branch := opts.Branch
	if branch == "" {
		var err error
		branch, err = repo.CurrentBranch()
		if err != nil {
			return "", err
		}
	}
	parent := opts.Base
	if parent == "" {
		return "", fmt.Errorf("PR base is required")
	}
	c := &Client{Dir: repo.Dir}

	if !repo.OriginBranchExists(branch) {
		fmt.Fprintf(os.Stderr, "stack: pushing %s to origin\n", branch)
		if err := repo.PushUpstream(branch); err != nil {
			return "", err
		}
	}

	if _, err := c.run("pr", "view", branch, "--json", "number", "--jq", ".number"); err == nil {
		currentBase, err := c.run("pr", "view", branch, "--json", "baseRefName", "--jq", ".baseRefName")
		if err != nil {
			return "", err
		}
		if currentBase != parent {
			if _, err := c.run("pr", "edit", branch, "--base", parent); err != nil {
				fmt.Fprintf(os.Stderr, "stack: could not retarget PR base (edit manually)\n")
			} else {
				fmt.Fprintf(os.Stderr, "stack: retargeted PR base: %s -> %s\n", currentBase, parent)
			}
		} else {
			fmt.Fprintf(os.Stderr, "stack: PR already targets %s\n", parent)
		}
	} else {
		args := []string{"pr", "create", "--base", parent, "--head", branch, "--fill"}
		if opts.Draft {
			args = append(args, "--draft")
		}
		cmd := exec.Command("gh", args...)
		if repo.Dir != "" {
			cmd.Dir = repo.Dir
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return "", err
		}
	}

	if len(opts.StackBranches) > 0 {
		if err := c.writeStackBody(branch, opts.StackBranches); err != nil {
			fmt.Fprintf(os.Stderr, "stack: could not update PR stack section: %v\n", err)
		}
	}

	return c.run("pr", "view", branch, "--json", "url", "--jq", ".url")
}

// writeStackBody builds stack markdown from open PRs and upserts it into the PR body.
func (c *Client) writeStackBody(branch string, stackBranches []string) error {
	prs, err := c.ListOpenPRs()
	if err != nil {
		prs = map[string]PRInfo{}
	}
	// Ensure the current PR is present even if list is briefly stale after create.
	if _, ok := prs[branch]; !ok {
		if info, err := c.viewPRInfo(branch); err == nil {
			prs[branch] = info
		}
	}
	md := FormatStackMarkdown(prs, stackBranches, branch)
	return c.updatePRBodyStack(branch, md)
}

func (c *Client) viewPRInfo(branch string) (PRInfo, error) {
	out, err := c.run("pr", "view", branch, "--json", "headRefName,baseRefName,number,url,isDraft")
	if err != nil {
		return PRInfo{}, err
	}
	var row struct {
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
		Number      int    `json:"number"`
		URL         string `json:"url"`
		IsDraft     bool   `json:"isDraft"`
	}
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		return PRInfo{}, fmt.Errorf("pr view parse: %w", err)
	}
	head := row.HeadRefName
	if head == "" {
		head = branch
	}
	return PRInfo{
		Head:   head,
		Base:   row.BaseRefName,
		Number: row.Number,
		URL:    row.URL,
		Draft:  row.IsDraft,
	}, nil
}

// HasPR reports whether an open (or any) PR exists for the branch head.
func HasPR(repo *git.Repo, branch string) bool {
	if !Available() {
		return false
	}
	c := &Client{Dir: repo.Dir}
	_, err := c.run("pr", "view", branch, "--json", "number", "--jq", ".number")
	return err == nil
}

// RetargetBase sets PR base if a PR exists for branch.
// Returns nil if gh is missing or no PR exists.
func RetargetBase(repo *git.Repo, branch, newBase string) error {
	if !Available() {
		return nil
	}
	c := &Client{Dir: repo.Dir}
	if _, err := c.run("pr", "view", branch, "--json", "number", "--jq", ".number"); err != nil {
		return nil
	}
	_, err := c.run("pr", "edit", branch, "--base", newBase)
	return err
}
