package stack

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/benwyrosdick/git-stack/internal/git"
)

// DeleteOpts for DeleteLocal.
type DeleteOpts struct {
	Branch string
	Force  bool // git branch -D instead of -d
}

// DeleteLocal deletes a local branch (-d or -D). Refuses trunk.
// If the branch is checked out, switches to its parent (or trunk) first.
// Clears local stack parent metadata for the deleted branch.
func (e *Engine) DeleteLocal(opts DeleteOpts) error {
	branch := opts.Branch
	if branch == "" {
		return fmt.Errorf("branch required")
	}
	if e.IsTrunk(branch) {
		return fmt.Errorf("refusing to delete trunk branch %s", branch)
	}
	if !e.Repo.LocalBranchExists(branch) {
		return fmt.Errorf("local branch does not exist: %s", branch)
	}

	// Switch away if needed.
	if cur, err := e.Repo.CurrentBranch(); err == nil && cur == branch {
		dest := e.ParentOf(branch)
		if dest == branch || !e.Repo.LocalBranchExists(dest) {
			dest = e.Repo.DefaultBranch()
		}
		if !e.Repo.LocalBranchExists(dest) {
			return fmt.Errorf("cannot leave branch %s: no checkout target", branch)
		}
		e.info("switching to %s before delete", dest)
		if err := e.Repo.Switch(dest); err != nil {
			return err
		}
	}

	var err error
	if opts.Force {
		e.info("force-deleting local branch %s", branch)
		err = e.Repo.ForceDeleteBranch(branch)
	} else {
		e.info("deleting local branch %s (safe)", branch)
		err = e.Repo.DeleteBranch(branch)
	}
	if err != nil {
		if !opts.Force {
			return fmt.Errorf("%w\n\n  Branch is not fully merged. Use force delete (D) if you are sure:\n    git branch -D %s", err, branch)
		}
		return err
	}
	_ = e.Repo.UnsetStackParent(branch)
	e.clearPRParentEntry(branch)
	e.info("deleted %s", branch)
	return nil
}

// Pull fetches origin and rebases the local branch onto origin/<branch>
// (git pull --rebase). In-sync / ahead are no-ops; behind or diverged rebase.
func (e *Engine) Pull(branch string) error {
	if branch == "" {
		var err error
		branch, err = e.Repo.CurrentBranch()
		if err != nil {
			return err
		}
	}
	if !e.Repo.LocalBranchExists(branch) {
		return fmt.Errorf("local branch does not exist: %s", branch)
	}
	if err := e.Repo.RequireNoRebase(); err != nil {
		return err
	}
	// Rebase (and checkout of a non-current branch) need a clean worktree.
	if err := e.Repo.RequireClean(); err != nil {
		return err
	}

	if err := e.FetchIfNeeded(false); err != nil {
		return err
	}
	if !e.Repo.OriginBranchExists(branch) {
		return fmt.Errorf("no origin/%s to pull from (push the branch first or check the name)", branch)
	}

	rel := e.Repo.RemoteRelationOf(branch)
	switch rel {
	case git.RelInSync:
		e.info("%s already in-sync with origin", branch)
		return nil
	case git.RelAhead:
		return fmt.Errorf("%s is ahead of origin (nothing to pull; push if you want to publish)", branch)
	case git.RelBehind, git.RelDiverged:
		if err := e.Repo.RebaseOntoOrigin(branch); err != nil {
			return fmt.Errorf("%w\n\nResolve conflicts, then: git rebase --continue\nOr abort: git rebase --abort", err)
		}
		short, _ := e.Repo.ShortSHA("refs/heads/" + branch)
		e.info("pulled (rebase) %s onto origin/%s (%s)", branch, branch, short)
		return nil
	default:
		return fmt.Errorf("cannot pull %s (remote relation: %s)", branch, rel)
	}
}

// Parent prints inferred parent of branch (default: current).
func (e *Engine) Parent(branch string) (string, error) {
	if branch == "" {
		var err error
		branch, err = e.Repo.CurrentBranch()
		if err != nil {
			return "", err
		}
	}
	return e.ParentOf(branch), nil
}

// CreateOpts for Create.
type CreateOpts struct {
	Name string
	From string // optional start-point override
}

// Create makes a new branch from inferred or explicit parent.
func (e *Engine) Create(opts CreateOpts) error {
	if opts.Name == "" {
		return fmt.Errorf("usage: git-stack create <name> [--from <start-point>]")
	}
	if e.Repo.RefExists(opts.Name) {
		return fmt.Errorf("branch already exists: %s", opts.Name)
	}
	if conflict, ok := e.SlashRefConflict(opts.Name); ok {
		return fmt.Errorf("cannot create '%s': git forbids a branch under existing branch '%s' (use dots for stack depth, e.g. %s.ui)", opts.Name, conflict, conflict)
	}
	parent := opts.From
	if parent == "" {
		parent = e.ParentOf(opts.Name)
	}
	if !e.Repo.RefExists(parent) {
		return fmt.Errorf("start-point does not exist: %s (create the parent branch first)", parent)
	}
	startRef, err := e.Repo.ResolveRef(parent)
	if err != nil {
		return err
	}
	if err := e.Repo.SwitchCreate(opts.Name, startRef); err != nil {
		return err
	}
	// Record explicit parent so free names (no dots) stay stacked.
	if err := e.SetParentLocal(opts.Name, parent); err != nil {
		return err
	}
	e.info("created %s from %s", opts.Name, parent)
	return nil
}

// RestackOpts for Restack.
type RestackOpts struct {
	Branch    string
	Push      bool
	OntoTrunk bool
	NoFetch   bool
}

// Restack replays branch onto parent (or ancestor chain with OntoTrunk).
func (e *Engine) Restack(opts RestackOpts) error {
	branch := opts.Branch
	if branch == "" {
		var err error
		branch, err = e.Repo.CurrentBranch()
		if err != nil {
			return err
		}
	}
	if err := e.Repo.RequireClean(); err != nil {
		return err
	}
	if err := e.Repo.RequireNoRebase(); err != nil {
		return err
	}
	if !e.Repo.LocalBranchExists(branch) {
		return fmt.Errorf("local branch does not exist: %s", branch)
	}
	if err := e.FetchIfNeeded(opts.NoFetch); err != nil {
		return err
	}

	var chain []string
	if opts.OntoTrunk {
		var err error
		chain, err = e.AncestorChainTo(branch)
		if err != nil {
			return err
		}
		if len(chain) == 0 {
			return fmt.Errorf("nothing to restack onto trunk (is '%s' the default branch?)", branch)
		}
		e.info("restack --onto-trunk: chain %s", strings.Join(chain, " "))
	} else {
		chain = []string{branch}
	}

	for _, b := range chain {
		if err := e.EnsureRemoteReady(b); err != nil {
			return wrapRemoteErr(fmt.Sprintf("fix '%s' vs origin before restacking", b), err)
		}
		parent := e.ParentOf(b)
		if e.Repo.LocalBranchExists(parent) {
			if err := e.EnsureRemoteReady(parent); err != nil {
				return wrapRemoteErr(fmt.Sprintf("fix parent '%s' vs origin before restacking", parent), err)
			}
		} else if opts.OntoTrunk && e.IsTrunk(parent) {
			if !e.Repo.RefExists(parent) {
				return fmt.Errorf("trunk does not exist: %s", parent)
			}
		} else if !e.Repo.RefExists(parent) {
			return fmt.Errorf("parent does not exist: %s", parent)
		}
	}

	for _, b := range chain {
		parent := e.ParentOf(b)
		pref := ""
		if opts.OntoTrunk && e.IsTrunk(parent) {
			var err error
			pref, err = e.TrunkRef()
			if err != nil {
				return err
			}
		}
		if err := e.RestackBranch(b, parent, pref); err != nil {
			return err
		}
		if err := e.MaybePush(b, opts.Push); err != nil {
			return err
		}
	}
	return nil
}

// ReparentOpts for Reparent.
type ReparentOpts struct {
	Branch    string
	NewParent string
	OldParent string // optional
	Push      bool
	NoFetch   bool
}

// Reparent moves branch onto a different parent via rebase --onto.
func (e *Engine) Reparent(opts ReparentOpts) error {
	if opts.Branch == "" || opts.NewParent == "" {
		return fmt.Errorf("usage: git-stack reparent <branch> <new-parent> [--from <old-parent>] [--push] [--no-fetch]")
	}
	if err := e.Repo.RequireClean(); err != nil {
		return err
	}
	if err := e.Repo.RequireNoRebase(); err != nil {
		return err
	}
	if !e.Repo.RefExists(opts.Branch) {
		return fmt.Errorf("branch does not exist: %s", opts.Branch)
	}
	if !e.Repo.RefExists(opts.NewParent) {
		return fmt.Errorf("new parent does not exist: %s", opts.NewParent)
	}
	oldParent := opts.OldParent
	if oldParent == "" {
		oldParent = e.ParentOf(opts.Branch)
	}
	if !e.Repo.RefExists(oldParent) {
		return fmt.Errorf("old parent does not exist: %s", oldParent)
	}
	if err := e.FetchIfNeeded(opts.NoFetch); err != nil {
		return err
	}

	if oldParent == opts.NewParent {
		e.info("%s parent is already %s; restacking instead", opts.Branch, opts.NewParent)
		return e.Restack(RestackOpts{
			Branch:  opts.Branch,
			Push:    opts.Push,
			NoFetch: true,
		})
	}

	if e.Repo.LocalBranchExists(opts.Branch) {
		if err := e.EnsureRemoteReady(opts.Branch); err != nil {
			return wrapRemoteErr(fmt.Sprintf("fix '%s' vs origin before reparenting", opts.Branch), err)
		}
	}
	if e.Repo.LocalBranchExists(opts.NewParent) {
		if err := e.EnsureRemoteReady(opts.NewParent); err != nil {
			return wrapRemoteErr(fmt.Sprintf("fix new parent '%s' vs origin before reparenting", opts.NewParent), err)
		}
	}

	oldRef, err := e.Repo.ResolveRef(oldParent)
	if err != nil {
		return err
	}
	newRef, err := e.Repo.ResolveRef(opts.NewParent)
	if err != nil {
		return err
	}

	e.info("reparenting %s: %s -> %s", opts.Branch, oldParent, opts.NewParent)
	if err := e.rebaseOnto(newRef, oldRef, opts.Branch); err != nil {
		return err
	}
	// Persist stack link independent of branch name.
	if err := e.SetParentLocal(opts.Branch, opts.NewParent); err != nil {
		return err
	}
	e.InvalidateParentCache()
	e.info("reparented %s onto %s", opts.Branch, opts.NewParent)
	return e.MaybePush(opts.Branch, opts.Push)
}

// SyncOpts for Sync.
type SyncOpts struct {
	Root      string
	Push      bool
	OntoTrunk bool
	DryRun    bool
	NoFetch   bool
}

// PlanRow is one line of a sync plan.
type PlanRow struct {
	Branch string
	Remote git.RemoteRelation
	Action string
}

// SyncResult is the planned/applied sync outcome.
type SyncResult struct {
	Plan     []PlanRow
	Blockers []string
}

// Sync plan-then-apply: FF, restack root, restack descendants.
func (e *Engine) Sync(opts SyncOpts) (*SyncResult, error) {
	root := opts.Root
	if root == "" {
		var err error
		root, err = e.Repo.CurrentBranch()
		if err != nil {
			return nil, err
		}
	}
	if err := e.Repo.RequireClean(); err != nil {
		return nil, err
	}
	if err := e.Repo.RequireNoRebase(); err != nil {
		return nil, err
	}
	if !e.Repo.LocalBranchExists(root) {
		return nil, fmt.Errorf("local branch does not exist: %s", root)
	}
	if err := e.FetchIfNeeded(opts.NoFetch); err != nil {
		return nil, err
	}

	kids, err := e.DescendantsOf(root)
	if err != nil {
		return nil, err
	}
	sortedKids := e.SortByDepth(kids)

	var backChain []string
	if opts.OntoTrunk {
		backChain, err = e.AncestorChainTo(root)
		if err != nil {
			return nil, err
		}
	} else {
		backChain = []string{root}
	}

	seen := map[string]bool{}
	var scope []string
	for _, b := range backChain {
		if !seen[b] {
			seen[b] = true
			scope = append(scope, b)
		}
	}
	for _, b := range sortedKids {
		if !seen[b] {
			seen[b] = true
			scope = append(scope, b)
		}
	}

	rootParent := e.ParentOf(root)
	result := &SyncResult{}
	var ffList []string
	var restackList []string // "branch|parent"
	relFor := map[string]git.RemoteRelation{}

	ontoNote, dryNote := "no", "no"
	if opts.OntoTrunk {
		ontoNote = "yes"
	}
	if opts.DryRun {
		dryNote = "yes"
	}
	e.info("plan for %s (onto-trunk=%s, dry-run=%s)", root, ontoNote, dryNote)

	for _, b := range scope {
		if !e.Repo.LocalBranchExists(b) {
			result.Blockers = append(result.Blockers, b+": missing local branch")
			relFor[b] = "missing"
			continue
		}
		rel := e.Repo.RemoteRelationOf(b)
		relFor[b] = rel
		if rel == git.RelDiverged {
			result.Blockers = append(result.Blockers, b+": diverged from origin")
		} else if rel == git.RelBehind {
			ffList = append(ffList, b)
		}
	}

	if e.Repo.LocalBranchExists(rootParent) {
		if opts.OntoTrunk || !e.IsTrunk(rootParent) {
			rel := e.Repo.RemoteRelationOf(rootParent)
			if rel == git.RelDiverged {
				result.Blockers = append(result.Blockers, rootParent+": diverged from origin")
			} else if rel == git.RelBehind {
				if !contains(ffList, rootParent) {
					ffList = append(ffList, rootParent)
				}
			}
		}
	} else if opts.OntoTrunk && e.IsTrunk(rootParent) {
		if !e.Repo.RefExists(rootParent) {
			result.Blockers = append(result.Blockers, rootParent+": missing trunk")
		}
	}

	willRestack := map[string]bool{}
	addRestack := func(b, parent string) {
		key := b + "|" + parent
		if !contains(restackList, key) {
			restackList = append(restackList, key)
			willRestack[b] = true
		}
	}

	effectiveTip := func(name string) (string, error) {
		if !e.Repo.LocalBranchExists(name) {
			return "", fmt.Errorf("missing")
		}
		if e.Repo.RemoteRelationOf(name) == git.RelBehind {
			return e.Repo.RevParse("refs/remotes/origin/" + name)
		}
		return e.Repo.RevParse("refs/heads/" + name)
	}

	scheduleIfNeeded := func(b string, force bool) {
		parent := e.ParentOf(b)
		if !e.Repo.RefExists(parent) && !(opts.OntoTrunk && e.IsTrunk(parent)) {
			result.Blockers = append(result.Blockers, b+": missing parent "+parent)
			return
		}
		if !force {
			var parentTip, childTip string
			var err error
			if opts.OntoTrunk && e.IsTrunk(parent) {
				tr, err2 := e.TrunkRef()
				if err2 != nil {
					result.Blockers = append(result.Blockers, err2.Error())
					return
				}
				parentTip, err = e.Repo.RevParse(tr)
			} else if e.Repo.LocalBranchExists(parent) && e.Repo.RemoteRelationOf(parent) == git.RelBehind {
				parentTip, err = e.Repo.RevParse("refs/remotes/origin/" + parent)
			} else {
				pref, err2 := e.Repo.ResolveRef(parent)
				if err2 != nil {
					result.Blockers = append(result.Blockers, b+": missing parent "+parent)
					return
				}
				parentTip, err = e.Repo.RevParse(pref)
			}
			if err != nil {
				return
			}
			childTip, err = effectiveTip(b)
			if err != nil {
				return
			}
			if e.Repo.IsAncestor(parentTip, childTip) {
				return
			}
		}
		addRestack(b, parent)
	}

	for _, b := range backChain {
		parent := e.ParentOf(b)
		if b == root && !opts.OntoTrunk && e.IsTrunk(parent) {
			continue
		}
		if !opts.OntoTrunk && b != root {
			continue
		}
		scheduleIfNeeded(b, willRestack[parent])
	}
	for _, b := range sortedKids {
		parent := e.ParentOf(b)
		scheduleIfNeeded(b, willRestack[parent])
	}

	for _, b := range scope {
		rel := relFor[b]
		if rel == "" {
			rel = git.RelNone
		}
		if rel == "missing" || rel == git.RelDiverged {
			result.Plan = append(result.Plan, PlanRow{Branch: b, Remote: rel, Action: "BLOCKER"})
			continue
		}
		var actions []string
		if rel == git.RelBehind {
			actions = append(actions, "ff")
		}
		parent := e.ParentOf(b)
		var note string
		if willRestack[b] {
			actions = append(actions, "restack→"+parent)
			switch {
			case willRestack[parent]:
				note = "parent will restack"
			case opts.OntoTrunk && e.IsTrunk(parent):
				note = "onto trunk"
				if tip, err := effectiveTip(b); err == nil {
					if tr, err2 := e.TrunkRef(); err2 == nil {
						if n, err3 := e.Repo.RevListCount(tip + ".." + tr); err3 == nil && n > 20 {
							note = fmt.Sprintf("onto trunk; warning: trunk +%d commits", n)
						}
					}
				}
			case b == root && !e.IsTrunk(parent):
				note = "fix root on parent first"
			}
		} else {
			if b == root && e.IsTrunk(parent) && !opts.OntoTrunk {
				note = "root (trunk skipped; pass --onto-trunk)"
			} else {
				note = "already on parent tip"
			}
		}
		action := "noop"
		if len(actions) > 0 {
			action = strings.Join(actions, ",")
		}
		if note != "" {
			action += " (" + note + ")"
		}
		result.Plan = append(result.Plan, PlanRow{Branch: b, Remote: rel, Action: action})
	}

	w := e.writer()
	for _, row := range result.Plan {
		fmt.Fprintf(w, "  %-28s remote=%-9s  %s\n", row.Branch, row.Remote, row.Action)
	}

	if len(result.Blockers) > 0 {
		e.info("aborting; fix blockers before apply:")
		for _, blk := range result.Blockers {
			fmt.Fprintf(w, "  - %s\n", blk)
			if strings.Contains(blk, ": diverged from origin") {
				dname := strings.SplitN(blk, ":", 2)[0]
				if e.Repo.LocalBranchExists(dname) && e.Repo.OriginBranchExists(dname) {
					fmt.Fprint(w, e.DivergePlaybook(dname))
				}
			}
		}
		return result, fmt.Errorf("sync blocked")
	}

	if opts.DryRun {
		e.info("dry-run complete (no changes)")
		return result, nil
	}

	for _, b := range ffList {
		if e.Repo.LocalBranchExists(b) && e.Repo.RemoteRelationOf(b) == git.RelBehind {
			if err := e.Repo.FFBranch(b); err != nil {
				return result, err
			}
			short, _ := e.Repo.ShortSHA("refs/remotes/origin/" + b)
			e.info("fast-forwarded %s → origin/%s (%s)", b, b, short)
		}
	}

	var finished []string
	for _, entry := range restackList {
		parts := strings.SplitN(entry, "|", 2)
		br, par := parts[0], parts[1]
		parentOverride := ""
		if opts.OntoTrunk && e.IsTrunk(par) {
			var err error
			parentOverride, err = e.TrunkRef()
			if err != nil {
				return result, err
			}
		}
		if err := e.RestackBranch(br, par, parentOverride); err != nil {
			e.info("stopped after conflict on %s", br)
			if len(finished) > 0 {
				e.info("already restacked: %s", strings.Join(finished, " "))
			}
			return result, err
		}
		finished = append(finished, br)
		if err := e.MaybePush(br, opts.Push); err != nil {
			return result, err
		}
	}

	if len(restackList) == 0 && len(ffList) == 0 {
		e.info("nothing to do under %s", root)
	} else {
		e.info("sync complete under %s", root)
	}
	return result, nil
}

func (e *Engine) writer() io.Writer {
	if e.Out != nil {
		return e.Out
	}
	return os.Stderr
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// BranchStatus is status for ls / TUI.
type BranchStatus string

const (
	StatusOK            BranchStatus = "ok"
	StatusNeedsRestack  BranchStatus = "needs-restack"
	StatusMissingParent BranchStatus = "missing-parent"
)

// BranchInfo is one row in the stack tree.
type BranchInfo struct {
	Name       string
	Parent     string
	ShortSHA   string
	OwnCommits string
	Status     BranchStatus
	Depth      int
	Remote     git.RemoteRelation
	// TreePrefix is ASCII connectors, e.g. "│   ├─ " (set by OrderAsTree).
	TreePrefix string
}

// List returns stack tree info under root (or all stacks if root empty).
// When root is empty, trunk is first, then every branch in a stack graph
// (PR parent, local parent, dots, or ancestor of such).
func (e *Engine) List(root string) ([]BranchInfo, error) {
	trunk := e.Repo.DefaultBranch()
	var list []string
	if root != "" {
		if !e.Repo.RefExists(root) {
			return nil, fmt.Errorf("branch does not exist: %s", root)
		}
		list = append(list, root)
		kids, err := e.DescendantsOf(root)
		if err != nil {
			return nil, err
		}
		list = append(list, e.SortByDepth(kids)...)
	} else {
		all, err := e.Repo.LocalBranches()
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		if e.Repo.RefExists(trunk) {
			list = append(list, trunk)
			seen[trunk] = true
		}
		// Collect stack members: non-trunk parent, or has stack descendants, or dots/config/PR.
		for _, b := range all {
			if seen[b] || e.IsTrunk(b) {
				continue
			}
			if e.inStackGraph(b) {
				seen[b] = true
				list = append(list, b)
				// include ancestors so free-named middles appear
				cur := e.ParentOf(b)
				for i := 0; i < 64 && cur != "" && !e.IsTrunk(cur) && !seen[cur]; i++ {
					if e.Repo.LocalBranchExists(cur) || e.Repo.RefExists(cur) {
						seen[cur] = true
						list = append(list, cur)
					}
					next := e.ParentOf(cur)
					if next == cur {
						break
					}
					cur = next
				}
			}
		}
		if len(list) <= 1 {
			e.info("no stacked local branches (create with --from or use dots, e.g. feature.ui)")
			if len(list) == 0 {
				return nil, nil
			}
		}
		rest := list[1:]
		rest = e.SortByDepth(rest)
		list = append([]string{list[0]}, rest...)
	}

	var infos []BranchInfo
	for _, b := range list {
		parent := e.ParentOf(b)
		short := "?"
		if ref, err := e.Repo.ResolveRef(b); err == nil {
			if s, err := e.Repo.ShortSHA(ref); err == nil {
				short = s
			}
		}
		own := "?"
		status := StatusMissingParent
		depth := e.BranchDepth(b)
		displayParent := parent

		if e.IsTrunk(b) {
			displayParent = "—"
			own = "0"
			status = StatusOK
			depth = 0
		} else if e.Repo.RefExists(parent) {
			pref, _ := e.Repo.ResolveRef(parent)
			bref, _ := e.Repo.ResolveRef(b)
			if n, err := e.Repo.RevListCount(pref + ".." + bref); err == nil {
				own = fmt.Sprintf("%d", n)
			}
			if e.Repo.IsAncestor(pref, bref) {
				status = StatusOK
			} else {
				status = StatusNeedsRestack
			}
		}
		rel := git.RelNone
		if e.Repo.LocalBranchExists(b) {
			rel = e.Repo.RemoteRelationOf(b)
		}
		infos = append(infos, BranchInfo{
			Name:       b,
			Parent:     displayParent,
			ShortSHA:   short,
			OwnCommits: own,
			Status:     status,
			Depth:      depth,
			Remote:     rel,
		})
	}
	return OrderAsTree(infos), nil
}

// inStackGraph reports whether branch participates in a stack (not only trunk).
func (e *Engine) inStackGraph(b string) bool {
	if e.IsTrunk(b) {
		return false
	}
	// Explicit local parent
	if e.Repo.GetStackParent(b) != "" {
		return true
	}
	// Open PR with a base
	if e.prParents != nil {
		if base, ok := e.prParents[b]; ok && base != "" {
			return true
		}
	}
	// Dot name convention
	if strings.Contains(b, ".") {
		return true
	}
	// Parent is non-trunk (via any resolution)
	p := e.ParentOf(b)
	if p != "" && !e.IsTrunk(p) {
		return true
	}
	// Has children in the graph
	kids, err := e.DescendantsOf(b)
	return err == nil && len(kids) > 0
}

// FormatList prints ls-style lines with ASCII tree connectors and aligned columns.
func FormatList(root string, infos []BranchInfo) string {
	// Ensure prefixes exist even if caller skipped OrderAsTree.
	if len(infos) > 0 && infos[0].TreePrefix == "" && len(infos) > 1 {
		infos = OrderAsTree(infos)
	}
	// Use display width (runes), not bytes — tree glyphs are multi-byte UTF-8.
	branchW, shaW, ownW, statusW, remoteW := 8, 7, 3, dispWidth("[needs-restack]"), dispWidth("diverged")
	for _, info := range infos {
		if n := dispWidth(info.TreePrefix) + dispWidth(info.Name); n > branchW {
			branchW = n
		}
		if n := dispWidth(info.ShortSHA); n > shaW {
			shaW = n
		}
		if n := dispWidth("+" + info.OwnCommits); n > ownW {
			ownW = n
		}
		if n := dispWidth("[" + string(info.Status) + "]"); n > statusW {
			statusW = n
		}
		if n := dispWidth(string(info.Remote)); n > remoteW {
			remoteW = n
		}
	}
	var b strings.Builder
	for _, info := range infos {
		branch := padDisplay(info.TreePrefix+info.Name, branchW)
		sha := padDisplay(info.ShortSHA, shaW)
		own := padDisplay("+"+info.OwnCommits, ownW)
		st := padDisplay("["+string(info.Status)+"]", statusW)
		rel := padDisplay(string(info.Remote), remoteW)
		fmt.Fprintf(&b, "%s  %s  %s  %s  %s  base:%s\n",
			branch, sha, own, st, rel, info.Parent)
	}
	return b.String()
}

// dispWidth is terminal columns for s (box-drawing runes are width 1).
func dispWidth(s string) int {
	return utf8.RuneCountInString(s)
}

func padDisplay(s string, w int) string {
	n := dispWidth(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}
