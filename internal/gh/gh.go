// Package gh wraps the GitHub CLI for stacked PR create/retarget.
package gh

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/benwyrosdick/git-stack/internal/git"
	"github.com/benwyrosdick/git-stack/internal/stack"
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
	Draft  bool
}

// EnsurePR creates or retargets a PR with inferred parent base.
func EnsurePR(eng *stack.Engine, repo *git.Repo, opts PROpts) (string, error) {
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
	parent := eng.ParentOf(branch)
	c := &Client{Dir: repo.Dir}

	if !repo.OriginBranchExists(branch) {
		fmt.Fprintf(os.Stderr, "stack: pushing %s to origin\n", branch)
		if err := repo.PushUpstream(branch); err != nil {
			return "", err
		}
	}

	// Does PR exist?
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
		return c.run("pr", "view", branch, "--json", "url", "--jq", ".url")
	}

	args := []string{"pr", "create", "--base", parent, "--head", branch, "--fill"}
	if opts.Draft {
		args = append(args, "--draft")
	}
	// create prints URL to stdout
	cmd := exec.Command("gh", args...)
	if repo.Dir != "" {
		cmd.Dir = repo.Dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return "", cmd.Run()
}

// RetargetBase sets PR base if a PR exists for branch.
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
