package stack

import (
	"fmt"
	"sort"
	"strings"

	"github.com/benwyrosdick/git-stack/internal/gh"
)

// ParentSource explains how ParentOf resolved a parent.
type ParentSource string

const (
	SourceTrunk ParentSource = "trunk"
	SourcePR    ParentSource = "pr"
	SourceLocal ParentSource = "local"
	SourceDots  ParentSource = "name"
)

// LoadParentsOpts controls bulk parent loading from GitHub.
type LoadParentsOpts struct {
	Offline bool // skip gh; use cache/local/dots only
	Refresh bool // force gh refresh even if cache is fresh
}

// LoadParents populates the PR parent map (bulk gh pr list + disk cache).
func (e *Engine) LoadParents(opts LoadParentsOpts) error {
	if e.prParents == nil {
		e.prParents = map[string]string{}
	}
	if opts.Offline {
		// Prefer disk cache if present (even if stale) for offline.
		if gd, err := e.Repo.GitDir(); err == nil {
			if m, ok := gh.LoadPRParentCache(gd, 0); ok {
				e.prParents = m
			}
		}
		return nil
	}
	if !gh.Available() {
		return nil
	}
	gd, err := e.Repo.GitDir()
	if err != nil {
		return nil
	}
	if !opts.Refresh {
		if m, ok := gh.LoadPRParentCache(gd, gh.CacheTTL); ok {
			e.prParents = m
			return nil
		}
	}
	c := &gh.Client{Dir: e.Repo.Dir}
	m, err := c.ListOpenPRParents()
	if err != nil {
		// Fall back to stale cache
		if cached, ok := gh.LoadPRParentCache(gd, 0); ok {
			e.prParents = cached
			e.info("could not refresh PR parents (%v); using cache", err)
			return nil
		}
		e.info("could not load PR parents: %v", err)
		return nil // non-fatal; local/dots still work
	}
	e.prParents = m
	_ = gh.SavePRParentCache(gd, m)
	return nil
}

// InvalidateParentCache drops in-memory and on-disk PR parent cache.
func (e *Engine) InvalidateParentCache() {
	e.prParents = nil
	e.invalidateIndex()
	if gd, err := e.Repo.GitDir(); err == nil {
		gh.InvalidatePRParentCache(gd)
	}
}

// ParentOf resolves stack parent:
// PR base → local gitstack-parent config → dot-depth inference → trunk.
func (e *Engine) ParentOf(branch string) string {
	p, _ := e.ParentOfWithSource(branch)
	return p
}

// ParentOfWithSource is ParentOf plus how it was resolved.
func (e *Engine) ParentOfWithSource(branch string) (string, ParentSource) {
	if e.idx != nil {
		if p, ok := e.idx.parent[branch]; ok {
			return p, e.idx.parentSrc[branch]
		}
	}

	trunk := e.trunk()
	if branch == "" || branch == trunk || e.IsTrunk(branch) {
		e.memoParent(branch, trunk, SourceTrunk)
		return trunk, SourceTrunk
	}

	// 1. Open PR base (shared)
	if e.prParents != nil {
		if base, ok := e.prParents[branch]; ok && base != "" {
			e.memoParent(branch, base, SourcePR)
			return base, SourcePR
		}
	}

	// 2. Local config (bulk-loaded when index is present)
	if local := e.configParentOf(branch); local != "" {
		e.memoParent(branch, local, SourceLocal)
		return local, SourceLocal
	}

	// 3. Dot-depth inference
	if p := parentFromDots(branch, e.refExists, trunk); p != trunk || strings.Contains(branch, ".") {
		if p != branch {
			e.memoParent(branch, p, SourceDots)
			return p, SourceDots
		}
	}

	e.memoParent(branch, trunk, SourceTrunk)
	return trunk, SourceTrunk
}

func (e *Engine) memoParent(branch, parent string, src ParentSource) {
	if e.idx == nil || branch == "" {
		return
	}
	e.idx.parent[branch] = parent
	e.idx.parentSrc[branch] = src
}

// parentFromDots walks off last "." segment until a ref exists; else trunk.
func parentFromDots(branch string, refExists func(string) bool, trunk string) string {
	if !strings.Contains(branch, ".") {
		return trunk
	}
	candidate := branch
	for strings.Contains(candidate, ".") {
		i := strings.LastIndex(candidate, ".")
		candidate = candidate[:i]
		if refExists(candidate) {
			return candidate
		}
	}
	return trunk
}

// DotBranchDepth is number of '.' + 1 (legacy name helper for display fallback).
func DotBranchDepth(b string) int {
	return strings.Count(b, ".") + 1
}

// BranchDepth is graph distance from trunk (0 = trunk), cycle-safe.
func (e *Engine) BranchDepth(branch string) int {
	if e.idx != nil {
		if d, ok := e.idx.depth[branch]; ok {
			return d
		}
	}
	trunk := e.trunk()
	if branch == trunk || e.IsTrunk(branch) {
		e.memoDepth(branch, 0)
		return 0
	}
	seen := map[string]bool{}
	depth := 0
	cur := branch
	for depth < 64 {
		if seen[cur] {
			e.memoDepth(branch, depth)
			return depth
		}
		seen[cur] = true
		p := e.ParentOf(cur)
		if p == cur || p == trunk || e.IsTrunk(p) {
			e.memoDepth(branch, depth+1)
			return depth + 1
		}
		cur = p
		depth++
	}
	e.memoDepth(branch, depth)
	return depth
}

func (e *Engine) memoDepth(branch string, d int) {
	if e.idx != nil {
		e.idx.depth[branch] = d
	}
}

// SetParentLocal records an explicit parent in local config.
// If parent is trunk or empty, unsets the key. Rejects cycles.
func (e *Engine) SetParentLocal(branch, parent string) error {
	if branch == "" {
		return fmt.Errorf("branch required")
	}
	trunk := e.Repo.DefaultBranch()
	if parent == "" || parent == trunk || e.IsTrunk(parent) {
		return e.Repo.UnsetStackParent(branch)
	}
	if parent == branch {
		return fmt.Errorf("branch cannot be its own parent")
	}
	if !e.Repo.RefExists(parent) {
		return fmt.Errorf("parent does not exist: %s", parent)
	}
	// Cycle: walking parent chain should not hit branch
	seen := map[string]bool{branch: true}
	cur := parent
	for i := 0; i < 64; i++ {
		if seen[cur] {
			return fmt.Errorf("cycle detected: setting %s → %s would cycle", branch, parent)
		}
		seen[cur] = true
		if cur == trunk || e.IsTrunk(cur) {
			break
		}
		// Use raw resolution without treating `branch` as already having new parent:
		// check PR/local/dots for cur only
		next := e.parentOfIgnoring(branch, cur)
		if next == cur {
			break
		}
		cur = next
	}
	return e.Repo.SetStackParent(branch, parent)
}

// parentOfIgnoring resolves parent for cur, but if we'd use local config for
// `ignore` branch, pretend it's unset (for cycle checks before write).
func (e *Engine) parentOfIgnoring(ignore, cur string) string {
	trunk := e.Repo.DefaultBranch()
	if cur == ignore {
		// For the branch we're about to reparent, use only PR/dots
		if e.prParents != nil {
			if base, ok := e.prParents[cur]; ok && base != "" {
				return base
			}
		}
		return parentFromDots(cur, e.Repo.RefExists, trunk)
	}
	return e.ParentOf(cur)
}

// DescendantsOf returns local branches that have root as an ancestor in the
// parent graph (not name prefix).
func (e *Engine) DescendantsOf(root string) ([]string, error) {
	if e.idx != nil {
		// BFS over precomputed children map — no extra git.
		var out []string
		queue := append([]string{}, e.idx.children[root]...)
		seen := map[string]bool{}
		for len(queue) > 0 {
			b := queue[0]
			queue = queue[1:]
			if seen[b] {
				continue
			}
			seen[b] = true
			out = append(out, b)
			queue = append(queue, e.idx.children[b]...)
		}
		return out, nil
	}
	all, err := e.Repo.LocalBranches()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, b := range all {
		if b == root {
			continue
		}
		if e.hasAncestor(b, root) {
			out = append(out, b)
		}
	}
	return out, nil
}

// hasAncestor reports whether ancestor appears in branch's ParentOf chain.
func (e *Engine) hasAncestor(branch, ancestor string) bool {
	trunk := e.trunk()
	seen := map[string]bool{}
	cur := branch
	for i := 0; i < 64; i++ {
		if seen[cur] {
			return false
		}
		seen[cur] = true
		p := e.ParentOf(cur)
		if p == ancestor {
			return true
		}
		if p == cur || p == trunk || e.IsTrunk(p) {
			return false
		}
		cur = p
	}
	return false
}

// SortByDepth sorts branch names by graph depth ascending, then name.
func (e *Engine) SortByDepth(branches []string) []string {
	out := append([]string(nil), branches...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := e.BranchDepth(out[i]), e.BranchDepth(out[j])
		if di != dj {
			return di < dj
		}
		return out[i] < out[j]
	})
	return out
}

// PRStackBranches returns the stack lineage for PR body display: trunk, ancestors
// of branch (shallow first), branch, then descendants (by depth). Used to render
// linked stack sections in PR descriptions.
func (e *Engine) PRStackBranches(branch string) []string {
	if branch == "" {
		return nil
	}
	trunk := e.Repo.DefaultBranch()
	out := []string{}
	if trunk != "" {
		out = append(out, trunk)
	}
	chain, err := e.AncestorChainTo(branch)
	if err != nil || len(chain) == 0 {
		// Still show trunk + branch when chain resolution fails.
		if branch != trunk && !e.IsTrunk(branch) {
			out = append(out, branch)
		}
	} else {
		out = append(out, chain...)
	}
	kids, err := e.DescendantsOf(branch)
	if err == nil && len(kids) > 0 {
		out = append(out, e.SortByDepth(kids)...)
	}
	// Dedup while preserving order (trunk might equal something weird).
	seen := map[string]bool{}
	dedup := make([]string, 0, len(out))
	for _, b := range out {
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		dedup = append(dedup, b)
	}
	return dedup
}

// AncestorChainTo returns local stack chain from base (child of trunk) → branch,
// shallow first. Does not include trunk.
func (e *Engine) AncestorChainTo(branch string) ([]string, error) {
	trunk := e.Repo.DefaultBranch()
	var deepFirst []string
	cur := branch
	seen := map[string]bool{}
	for cur != trunk && !e.IsTrunk(cur) {
		if seen[cur] {
			return nil, fmt.Errorf("cycle in parent chain at %s", cur)
		}
		seen[cur] = true
		if !e.Repo.LocalBranchExists(cur) {
			return nil, fmt.Errorf("local branch does not exist in stack chain: %s", cur)
		}
		deepFirst = append(deepFirst, cur)
		p := e.ParentOf(cur)
		if p == cur {
			return nil, fmt.Errorf("cannot resolve parent chain at %s", cur)
		}
		if p == trunk || e.IsTrunk(p) {
			break
		}
		cur = p
	}
	for i, j := 0, len(deepFirst)-1; i < j; i, j = i+1, j-1 {
		deepFirst[i], deepFirst[j] = deepFirst[j], deepFirst[i]
	}
	return deepFirst, nil
}

// Track sets local parent metadata without rebasing.
// Also retargets an open PR base when possible, because PR base wins over local
// config in ParentOf resolution (shared team source of truth).
func (e *Engine) Track(branch, parent string) error {
	if branch == "" {
		var err error
		branch, err = e.Repo.CurrentBranch()
		if err != nil {
			return err
		}
	}
	if !e.Repo.LocalBranchExists(branch) && !e.Repo.RefExists(branch) {
		return fmt.Errorf("branch does not exist: %s", branch)
	}
	if !e.Repo.RefExists(parent) {
		return fmt.Errorf("parent does not exist: %s", parent)
	}
	if err := e.SetParentLocal(branch, parent); err != nil {
		return err
	}
	e.invalidateIndex()
	e.info("set local parent %s → %s", branch, parent)

	// PR base outranks local config. If a PR exists, retarget it and update the
	// cached PR map so ParentOf sees the new parent immediately.
	hadPR := e.prParents != nil && e.prParents[branch] != ""
	if gh.Available() && (hadPR || gh.HasPR(e.Repo, branch)) {
		if err := gh.RetargetBase(e.Repo, branch, parent); err != nil {
			e.info("could not retarget PR base: %v", err)
			e.info("fix with: gh pr edit %s --base %s", branch, parent)
			// Drop stale PR map entry so local config can win until next refresh.
			e.clearPRParentEntry(branch)
		} else {
			e.info("retargeted PR base → %s", parent)
			e.writeThroughPRParent(branch, parent)
		}
	} else {
		// No PR: ensure a stale PR-cache entry cannot shadow local config.
		e.clearPRParentEntry(branch)
	}

	p, src := e.ParentOfWithSource(branch)
	want := parent
	if e.IsTrunk(parent) {
		want = e.Repo.DefaultBranch()
	}
	if p != want {
		e.info("warning: effective parent is still %s (%s), not %s", p, src, want)
		if src == SourcePR {
			e.info("an open PR still targets %s — run: gh pr edit %s --base %s", p, branch, parent)
		}
		return nil
	}
	e.info("tracked %s → parent %s (%s)", branch, p, src)
	return nil
}

// writeThroughPRParent updates the in-memory map and on-disk cache entry for one branch.
func (e *Engine) writeThroughPRParent(branch, parent string) {
	if e.prParents == nil {
		e.prParents = map[string]string{}
	}
	if e.IsTrunk(parent) {
		delete(e.prParents, branch)
	} else {
		e.prParents[branch] = parent
	}
	gd, err := e.Repo.GitDir()
	if err != nil {
		return
	}
	m, ok := gh.LoadPRParentCache(gd, 0)
	if !ok {
		m = map[string]string{}
	}
	if e.IsTrunk(parent) {
		delete(m, branch)
	} else {
		m[branch] = parent
	}
	_ = gh.SavePRParentCache(gd, m)
}

// clearPRParentEntry drops one branch from the PR parent map/cache.
func (e *Engine) clearPRParentEntry(branch string) {
	if e.prParents != nil {
		delete(e.prParents, branch)
	}
	gd, err := e.Repo.GitDir()
	if err != nil {
		return
	}
	m, ok := gh.LoadPRParentCache(gd, 0)
	if !ok {
		return
	}
	delete(m, branch)
	_ = gh.SavePRParentCache(gd, m)
}

// Untrack clears local parent metadata.
func (e *Engine) Untrack(branch string) error {
	if branch == "" {
		var err error
		branch, err = e.Repo.CurrentBranch()
		if err != nil {
			return err
		}
	}
	if err := e.Repo.UnsetStackParent(branch); err != nil {
		return err
	}
	e.invalidateIndex()
	e.info("untracked local parent for %s (now: %s)", branch, e.ParentOf(branch))
	return nil
}

// Adopt writes local parent config from current resolution for branch and descendants.
func (e *Engine) Adopt(root string) error {
	if root == "" {
		var err error
		root, err = e.Repo.CurrentBranch()
		if err != nil {
			return err
		}
	}
	targets := []string{root}
	kids, err := e.DescendantsOf(root)
	if err != nil {
		return err
	}
	targets = append(targets, kids...)
	n := 0
	for _, b := range targets {
		if e.IsTrunk(b) {
			continue
		}
		p := e.ParentOf(b)
		// Force write local even if from PR/dots
		if e.IsTrunk(p) {
			_ = e.Repo.UnsetStackParent(b)
			continue
		}
		if err := e.Repo.SetStackParent(b, p); err != nil {
			return err
		}
		n++
		e.info("adopted %s → %s", b, p)
	}
	e.info("adopted %d branch parent(s) under %s", n, root)
	return nil
}
