// Package stack implements plain-git stacked branch helpers.
// Parents resolve from PR base → local config → dot-depth name → trunk.
package stack

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/benwyrosdick/git-stack/internal/git"
)

// Engine runs stack operations against a git repo.
type Engine struct {
	Repo   *git.Repo
	Out    io.Writer // info messages (stderr-like); defaults to os.Stderr
	Quiet  bool      // suppress interactive rebase attach (use quiet rebase)
	NoPush bool      // ignored; push is per-call

	// prParents is head → base from open PRs (bulk-loaded via LoadParents).
	prParents map[string]string

	// idx is a bulk-loaded memo for fast List / ParentOf (see index.go).
	idx *repoIndex
}

func (e *Engine) info(format string, args ...any) {
	w := e.Out
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "stack: "+format+"\n", args...)
}

func (e *Engine) rebaseOnto(onto, upstream, branch string) error {
	if e.Quiet {
		return e.Repo.RebaseOntoQuiet(onto, upstream, branch)
	}
	return e.Repo.RebaseOnto(onto, upstream, branch)
}

// SlashRefConflict returns an existing ancestor path segment if name uses /
// under an existing branch (git forbids nesting).
func (e *Engine) SlashRefConflict(name string) (string, bool) {
	if !strings.Contains(name, "/") {
		return "", false
	}
	candidate := name
	for strings.Contains(candidate, "/") {
		i := strings.LastIndex(candidate, "/")
		candidate = candidate[:i]
		if e.Repo.RefExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// IsTrunk reports whether name is the default branch.
func (e *Engine) IsTrunk(name string) bool {
	return name == e.trunk()
}

// TrunkRef prefers origin/trunk, else local trunk.
func (e *Engine) TrunkRef() (string, error) {
	trunk := e.Repo.DefaultBranch()
	if e.Repo.OriginBranchExists(trunk) {
		return "refs/remotes/origin/" + trunk, nil
	}
	if e.Repo.LocalBranchExists(trunk) {
		return "refs/heads/" + trunk, nil
	}
	return "", fmt.Errorf("trunk branch does not exist: %s", trunk)
}

// ---------------------------------------------------------------------------
// Restack core
// ---------------------------------------------------------------------------

// RestackUpstream returns the cutoff SHA: only commits after this are replayed.
// Prefers fork-point so rewritten parents do not pull old parent commits.
func (e *Engine) RestackUpstream(parentRef, branchRef string) (string, error) {
	if base, err := e.Repo.MergeBaseForkPoint(parentRef, branchRef); err == nil && base != "" {
		return base, nil
	}
	return e.Repo.MergeBase(parentRef, branchRef)
}

// RestackBranch restacks local branch onto parent tip.
// parentRefOverride is optional (e.g. refs/remotes/origin/main for --onto-trunk).
func (e *Engine) RestackBranch(branch, parent, parentRefOverride string) error {
	if parent == "" {
		parent = e.ParentOf(branch)
	}
	if !e.Repo.LocalBranchExists(branch) {
		return fmt.Errorf("local branch does not exist: %s", branch)
	}

	var parentRef string
	var err error
	if parentRefOverride != "" {
		parentRef = parentRefOverride
		if _, err = e.Repo.RevParse(parentRef); err != nil {
			return fmt.Errorf("parent ref does not exist: %s", parentRef)
		}
	} else {
		if !e.Repo.RefExists(parent) {
			return fmt.Errorf("parent does not exist: %s", parent)
		}
		parentRef, err = e.Repo.ResolveRef(parent)
		if err != nil {
			return err
		}
	}

	branchRef := "refs/heads/" + branch
	parentTip, err := e.Repo.RevParse(parentRef)
	if err != nil {
		return err
	}
	branchTip, err := e.Repo.RevParse(branchRef)
	if err != nil {
		return err
	}

	if branchTip == parentTip {
		e.info("%s already points at %s (nothing to restack)", branch, parent)
		return nil
	}
	if e.Repo.IsAncestor(parentRef, branchRef) {
		e.info("%s already based on tip of %s", branch, parent)
		return nil
	}

	upstream, err := e.RestackUpstream(parentRef, branchRef)
	if err != nil {
		return err
	}
	if upstream == parentTip {
		e.info("%s already based on tip of %s", branch, parent)
		return nil
	}

	n, _ := e.Repo.RevListCount(upstream + ".." + branchRef)
	short := upstream
	if len(short) > 8 {
		short = short[:8]
	}
	e.info("restacking %s onto %s (replay %d commit(s) after %s)", branch, parent, n, short)
	if err := e.rebaseOnto(parentRef, upstream, branch); err != nil {
		return fmt.Errorf("conflict restacking %s onto %s\n\n  Fix: resolve conflicts, then: git rebase --continue\n  Abort this branch: git rebase --abort\n  Re-run when clean: git-stack sync <root>   # or git-stack restack %s", branch, parent, branch)
	}
	e.info("restacked %s onto %s", branch, parent)
	return nil
}

// BranchNeedsRestack reports whether branch is not based on parent tip.
func (e *Engine) BranchNeedsRestack(branch, parent, parentRefOverride string) bool {
	if parent == "" {
		parent = e.ParentOf(branch)
	}
	if !e.Repo.LocalBranchExists(branch) {
		return true
	}
	var parentRef string
	if parentRefOverride != "" {
		parentRef = parentRefOverride
	} else {
		if !e.Repo.RefExists(parent) {
			return true
		}
		var err error
		parentRef, err = e.Repo.ResolveRef(parent)
		if err != nil {
			return true
		}
	}
	return !e.Repo.IsAncestor(parentRef, "refs/heads/"+branch)
}

// ---------------------------------------------------------------------------
// Remote safety
// ---------------------------------------------------------------------------

// DivergePlaybook returns recovery help for a diverged branch (multiline).
func (e *Engine) DivergePlaybook(branch string) string {
	l, _ := e.Repo.RevParse("refs/heads/" + branch)
	rr, _ := e.Repo.RevParse("refs/remotes/origin/" + branch)
	onlyLocal, _ := e.Repo.RevListOneline(rr + ".." + l)
	onlyRemote, _ := e.Repo.RevListOneline(l + ".." + rr)
	if onlyLocal == "" {
		onlyLocal = "(none)"
	} else {
		onlyLocal = indentBlock(onlyLocal, "    ")
	}
	if onlyRemote == "" {
		onlyRemote = "(none)"
	} else {
		onlyRemote = indentBlock(onlyRemote, "    ")
	}
	ls, _ := e.Repo.ShortSHA(l)
	rs, _ := e.Repo.ShortSHA(rr)
	return fmt.Sprintf(`cannot cleanly proceed — branch '%s' has diverged from origin

  local:  %s  (commits not on origin below)
  origin: %s  (commits not local below)

  Commits only local:
%s

  Commits only on origin:
%s

  Resolve, then re-run:
    # prefer remote tip, then re-apply local work
    git switch %s && git reset --hard origin/%s
    # or put local commits on top of origin:
    git switch %s && git rebase origin/%s
`, branch, ls, rs, onlyLocal, onlyRemote, branch, branch, branch, branch)
}

func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// wrapRemoteErr joins a short action hint with a (possibly multiline) detail.
// Uses a newline so playbooks stay readable on CLI and in the TUI.
func wrapRemoteErr(summary string, err error) error {
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(err.Error())
	if detail == "" {
		return fmt.Errorf("%s", summary)
	}
	return fmt.Errorf("%s\n\n%s", summary, detail)
}

// EnsureRemoteReady FFs if behind; returns error (with playbook) if diverged.
func (e *Engine) EnsureRemoteReady(branch string) error {
	rel := e.Repo.RemoteRelationOf(branch)
	switch rel {
	case git.RelDiverged:
		return fmt.Errorf("%s", e.DivergePlaybook(branch))
	case git.RelBehind:
		if err := e.Repo.FFBranch(branch); err != nil {
			return err
		}
		short, _ := e.Repo.ShortSHA("refs/remotes/origin/" + branch)
		e.info("fast-forwarded %s → origin/%s (%s)", branch, branch, short)
	}
	return nil
}

// MaybePush pushes if doPush is true.
func (e *Engine) MaybePush(branch string, doPush bool) error {
	if !doPush {
		return nil
	}
	if e.Repo.OriginBranchExists(branch) {
		if err := e.Repo.PushForceWithLease(branch); err != nil {
			return err
		}
	} else {
		if err := e.Repo.PushUpstream(branch); err != nil {
			return err
		}
	}
	e.info("pushed %s", branch)
	return nil
}

// FetchIfNeeded fetches origin unless noFetch.
func (e *Engine) FetchIfNeeded(noFetch bool) error {
	if noFetch {
		return nil
	}
	if !e.Repo.HasOrigin() {
		e.info("no origin remote; skip fetch")
		return nil
	}
	e.info("fetching origin")
	return e.Repo.FetchOrigin()
}
