package stack

import "sort"

// OrderAsTree reorders infos into DFS tree order and sets TreePrefix
// (├─ └─ │) based on parent relationships among the listed branches.
func OrderAsTree(infos []BranchInfo) []BranchInfo {
	if len(infos) == 0 {
		return infos
	}
	byName := make(map[string]BranchInfo, len(infos))
	inList := make(map[string]bool, len(infos))
	for _, info := range infos {
		byName[info.Name] = info
		inList[info.Name] = true
	}

	children := make(map[string][]string)
	var roots []string
	for _, info := range infos {
		p := info.Parent
		if p == "" || p == "—" || !inList[p] {
			roots = append(roots, info.Name)
			continue
		}
		children[p] = append(children[p], info.Name)
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
	seen := make(map[string]bool, len(infos))

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
	// Orphans already marked roots; any missed (cycles) append plain
	for _, info := range infos {
		if !seen[info.Name] {
			info.TreePrefix = ""
			out = append(out, info)
		}
	}
	return out
}
