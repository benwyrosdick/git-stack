package stack

import (
	"strings"

	"github.com/benwyrosdick/git-stack/internal/git"
)

// repoIndex is a per-Engine memo of git facts for fast stack listing.
// Built with a handful of bulk git calls; ParentOf and List reuse it.
type repoIndex struct {
	trunk string

	localTips  map[string]string // branch → full sha
	originTips map[string]string // branch → full sha (origin/<branch>)
	locals     []string

	// Explicit gitstack-parent from config (bulk).
	configParent map[string]string

	// Memoized ParentOf results.
	parent    map[string]string
	parentSrc map[string]ParentSource

	// parent → children (local branches only), built after parents resolved.
	children map[string][]string

	// Memoized depths.
	depth map[string]int
}

// ensureIndex loads bulk ref tips + config and prepares memo maps.
func (e *Engine) ensureIndex() error {
	if e.idx != nil {
		return nil
	}
	idx := &repoIndex{
		localTips:    map[string]string{},
		originTips:   map[string]string{},
		configParent: map[string]string{},
		parent:       map[string]string{},
		parentSrc:    map[string]ParentSource{},
		children:     map[string][]string{},
		depth:        map[string]int{},
	}

	// Trunk (1–3 git calls once).
	idx.trunk = e.Repo.DefaultBranch()

	// All local tips — 1 git call.
	localTips, err := e.Repo.ListRefTips("refs/heads/")
	if err != nil {
		return err
	}
	idx.localTips = localTips
	idx.locals = make([]string, 0, len(localTips))
	for name := range localTips {
		idx.locals = append(idx.locals, name)
	}

	// Origin tips — 1 git call (optional).
	if originTips, err := e.Repo.ListRefTips("refs/remotes/origin/"); err == nil {
		for name, sha := range originTips {
			if name == "origin" || name == "origin/HEAD" {
				continue
			}
			b := strings.TrimPrefix(name, "origin/")
			if b == "" || b == "HEAD" {
				continue
			}
			idx.originTips[b] = sha
		}
	}

	// Stack parent config — 1 git call.
	if cfg, err := e.Repo.ListStackParentConfig(); err == nil {
		idx.configParent = cfg
	}

	e.idx = idx

	// Resolve ParentOf for every local branch once, then invert to children.
	for _, b := range idx.locals {
		_ = e.ParentOf(b)
	}
	idx.children = map[string][]string{}
	for b, p := range idx.parent {
		if b == idx.trunk || e.IsTrunk(b) {
			continue
		}
		if p == "" {
			continue
		}
		idx.children[p] = append(idx.children[p], b)
	}
	return nil
}

// invalidateIndex drops the memo so the next List rebuilds from git.
func (e *Engine) invalidateIndex() {
	e.idx = nil
}

func (e *Engine) trunk() string {
	if e.idx != nil && e.idx.trunk != "" {
		return e.idx.trunk
	}
	return e.Repo.DefaultBranch()
}

func (e *Engine) localExists(name string) bool {
	if e.idx != nil {
		_, ok := e.idx.localTips[name]
		return ok
	}
	return e.Repo.LocalBranchExists(name)
}

func (e *Engine) originExists(name string) bool {
	if e.idx != nil {
		_, ok := e.idx.originTips[name]
		return ok
	}
	return e.Repo.OriginBranchExists(name)
}

func (e *Engine) refExists(name string) bool {
	if e.localExists(name) || e.originExists(name) {
		return true
	}
	if e.idx != nil {
		// Index is authoritative for heads/origin; skip extra git for unknowns.
		return false
	}
	return e.Repo.RefExists(name)
}

func (e *Engine) tipSHA(name string) (string, bool) {
	if e.idx != nil {
		if s, ok := e.idx.localTips[name]; ok {
			return s, true
		}
		if s, ok := e.idx.originTips[name]; ok {
			return s, true
		}
		return "", false
	}
	if ref, err := e.Repo.ResolveRef(name); err == nil {
		if s, err := e.Repo.RevParse(ref); err == nil {
			return s, true
		}
	}
	return "", false
}

func (e *Engine) shortTip(name string) string {
	if sha, ok := e.tipSHA(name); ok && len(sha) >= 7 {
		return sha[:7]
	}
	if sha, ok := e.tipSHA(name); ok {
		return sha
	}
	return "?"
}

func (e *Engine) configParentOf(branch string) string {
	if e.idx != nil {
		return e.idx.configParent[branch]
	}
	return e.Repo.GetStackParent(branch)
}

// hasStackChildren reports whether any local branch has b as stack parent.
func (e *Engine) hasStackChildren(b string) bool {
	if e.idx != nil {
		return len(e.idx.children[b]) > 0
	}
	// Fallback without index (should be rare after List).
	kids, err := e.DescendantsOf(b)
	return err == nil && len(kids) > 0
}

// remoteRelationCached uses indexed tips; falls back to git merge-base only when tips differ.
func (e *Engine) remoteRelationCached(branch string) git.RemoteRelation {
	if !e.localExists(branch) {
		return git.RelNone
	}
	l, okL := e.tipSHA(branch)
	if !okL {
		return e.Repo.RemoteRelationOf(branch)
	}
	r, okR := "", false
	if e.idx != nil {
		r, okR = e.idx.originTips[branch]
	} else {
		okR = e.Repo.OriginBranchExists(branch)
		if okR {
			r, _ = e.Repo.RevParse("refs/remotes/origin/" + branch)
		}
	}
	if !okR || r == "" {
		return git.RelNone
	}
	if l == r {
		return git.RelInSync
	}
	// Need ancestry for ahead/behind/diverged.
	if e.Repo.IsAncestor(l, r) {
		return git.RelBehind
	}
	if e.Repo.IsAncestor(r, l) {
		return git.RelAhead
	}
	return git.RelDiverged
}
