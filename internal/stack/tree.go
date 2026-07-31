package stack

import (
	"sort"
	"strings"
)

// OrderAsTree reorders infos into DFS tree order and sets TreePrefix
// (├─ └─ │) based on parent relationships among the listed branches.
//
// Each branch name appears at most once. If infos contains duplicates
// (same Name), the entry with the higher PRNumber wins; ties keep the
// later entry. Parent/children edges are also de-duplicated.
func OrderAsTree(infos []BranchInfo) []BranchInfo {
	if len(infos) == 0 {
		return infos
	}
	byName := make(map[string]BranchInfo, len(infos))
	inList := make(map[string]bool, len(infos))
	for _, info := range infos {
		name := normalizeBranchName(info.Name)
		if name == "" {
			continue
		}
		info.Name = name
		if info.Parent != "" && info.Parent != "—" {
			info.Parent = normalizeBranchName(info.Parent)
		}
		if prev, ok := byName[name]; ok {
			// Prefer the row that knows about an open PR.
			if info.PRNumber <= prev.PRNumber {
				continue
			}
		}
		byName[name] = info
		inList[name] = true
	}

	children := make(map[string][]string)
	childSeen := make(map[string]map[string]bool) // parent → set of kids
	var roots []string
	rootSeen := make(map[string]bool)

	// Iterate unique names in sorted order for deterministic edges.
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		info := byName[name]
		p := info.Parent
		if p == "" || p == "—" || !inList[p] {
			if !rootSeen[name] {
				rootSeen[name] = true
				roots = append(roots, name)
			}
			continue
		}
		if childSeen[p] == nil {
			childSeen[p] = map[string]bool{}
		}
		if childSeen[p][name] {
			continue
		}
		childSeen[p][name] = true
		children[p] = append(children[p], name)
	}
	for p := range children {
		sort.Strings(children[p])
	}
	// Stable roots: trunk-like first (depth 0), then name
	sort.SliceStable(roots, func(i, j int) bool {
		di, dj := byName[roots[i]].Depth, byName[roots[j]].Depth
		if di != dj {
			return di < dj
		}
		return roots[i] < roots[j]
	})

	var out []BranchInfo
	seen := make(map[string]bool, len(byName))

	var walk func(name, prefix string, isRoot, isLast bool)
	walk = func(name, prefix string, isRoot, isLast bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		info := byName[name]
		conn := ""
		if !isRoot {
			if isLast {
				conn = "└─ "
			} else {
				conn = "├─ "
			}
		}
		info.TreePrefix = prefix + conn
		out = append(out, info)

		kids := children[name]
		for i, kid := range kids {
			last := i == len(kids)-1
			nextPrefix := prefix
			if !isRoot {
				if isLast {
					nextPrefix += "   "
				} else {
					nextPrefix += "│  "
				}
			}
			walk(kid, nextPrefix, false, last)
		}
	}

	for i, r := range roots {
		walk(r, "", true, i == len(roots)-1)
	}
	// Any missed (cycles) append plain, still unique.
	for _, name := range names {
		if !seen[name] {
			info := byName[name]
			info.TreePrefix = ""
			out = append(out, info)
			seen[name] = true
		}
	}
	return out
}

// normalizeBranchName trims whitespace from ref short names.
func normalizeBranchName(s string) string {
	return strings.TrimSpace(s)
}
