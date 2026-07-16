# git-stack

Lightweight CLI + TUI for **stacked branches / PRs** in plain git.

Parents are inferred from **dot depth** in the branch name (not `/`, because git cannot store both `foo` and `foo/bar`):

```
main
└── wms-batching           # PR base: main
    └── wms-batching.ui    # PR base: wms-batching
        └── wms-batching.ui.tests
```

Ported from battle-tested helpers used at Vesyl (`local-services/stack`), with the same restack/sync safety model.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/benwyrosdick/git-stack/main/scripts/install.sh | bash
```

Installs to `~/.local/bin` by default. Override with `GIT_STACK_INSTALL_DIR`.

From source (module root or elsewhere):

```bash
# from this repo
go install ./cmd/git-stack

# or without cloning
go install github.com/benwyrosdick/git-stack/cmd/git-stack@latest
```

(`go install` alone fails — there is no `main` package at the repo root.)

## Usage

```bash
git-stack                  # interactive TUI
git-stack ls [root]
git-stack parent [branch]
git-stack create <name> [--from <start>]
git-stack restack [branch] [--push] [--onto-trunk] [--no-fetch]
git-stack reparent <branch> <new-parent> [--from <old>] [--push] [--no-fetch]
git-stack sync [root] [--push] [--onto-trunk] [--dry-run] [--no-fetch]
git-stack pr [branch] [--draft]   # needs gh
```

### When to use restack vs sync

| Direction | Command | Does |
| --- | --- | --- |
| **Back** (ancestors) | `restack` | Put **me** on my parent tip |
| **Back** + trunk | `restack --onto-trunk` | Restack stack base → … → me onto path from trunk |
| **Forward** (descendants) | `sync` | Fix **root on its parent** (if parent ≠ trunk), then all kids under root |
| **Forward** + trunk | `sync --onto-trunk` | Backward chain from trunk through root, then kids |

Trunk (`main`) is **never** absorbed unless `--onto-trunk`.

### Everyday workflow

```bash
git-stack create wms-batching && git push -u origin HEAD && git-stack pr
git-stack create wms-batching.ui && git push -u origin HEAD && git-stack pr

git-stack restack wms-batching.ui --push          # one child, looking back
git-stack sync wms-batching --push                # whole stack under base
git-stack sync wms-batching --onto-trunk --push   # also absorb latest main
```

### TUI keys

| Key | Action |
| --- | --- |
| `j`/`k` | Move |
| `enter` | Checkout |
| `r` / `R` | Restack / restack `--onto-trunk` |
| `s` / `S` | Sync / sync `--onto-trunk` |
| `d` | Dry-run sync |
| `p` | Push (`--force-with-lease`) |
| `c` | Create child branch |
| `P` | Open/retarget PR |
| `f` | Fetch origin |
| `?` | Help |
| `q` | Quit |

## Safety

- Tracked dirty worktree / rebase-in-progress refused (untracked files OK)
- Diverged local/origin blocks the plan (playbook printed; no half-apply)
- Content conflicts still stop mid-apply after a clean plan
- Push opt-in via `--push` (`--force-with-lease` when remote exists)
- Restack cutoff prefers `git merge-base --fork-point` so force-pushed parents do not drag old parent commits into the child

## Development

```bash
go test ./...
go build -o git-stack ./cmd/git-stack
```

## License

MIT
