# git-stack

Lightweight CLI + TUI for **stacked branches / PRs** in plain git.

Parents are **not only** encoded in branch names. Resolution order:

1. **Open PR base** (shared with the team — draft PRs count)
2. **Local config** `branch.<name>.gitstack-parent`
3. **Dot-depth name** (`feature.ui` → `feature`) — zero-config fallback
4. **Trunk** (default branch)

You can reparent in or out of a stack **without renaming**.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/benwyrosdick/git-stack/main/scripts/install.sh | bash
```

From source:

```bash
go install ./cmd/git-stack
# or
go install github.com/benwyrosdick/git-stack/cmd/git-stack@latest
```

## Usage

```bash
git-stack                  # interactive TUI
git-stack ls [root]
git-stack parent [branch] [-v]          # -v shows source: pr|local|name|trunk
git-stack create <name> [--from <start>]
git-stack restack [branch] [--push] [--onto-trunk] [--no-fetch]
git-stack reparent <branch> <new-parent> [--from <old>] [--push] [--no-fetch]
git-stack sync [root] [--push] [--onto-trunk] [--dry-run] [--no-fetch]
git-stack pr [branch] [--draft]
git-stack track <branch> --parent <parent>   # metadata only
git-stack untrack [branch]
git-stack adopt [root]                       # write local parents from current resolution
```

Global flags: `--offline` (no `gh`), `--refresh` (force PR parent map reload).

### Free names (recommended for reparenting)

```bash
git-stack create api-work
git-stack create ui-work --from api-work   # records local parent
git push -u origin HEAD && git-stack pr --draft   # PR base = team-shared parent

git-stack reparent ui-work other-api       # move stacks without rename
```

### Dot names (still work)

```
main
└── wms-batching           # parent: main
    └── wms-batching.ui    # parent: wms-batching
```

### restack vs sync

| Direction | Command | Does |
| --- | --- | --- |
| **Back** | `restack` | Put **me** on my parent tip |
| **Back** + trunk | `restack --onto-trunk` | Restack chain onto trunk path |
| **Forward** | `sync` | Fix root on parent, then descendants |
| **Forward** + trunk | `sync --onto-trunk` | Include trunk |

`--dry-run` on sync **prints the plan only** (no branch rewrites).

Trunk is never absorbed unless `--onto-trunk`.

### TUI keys

| Key | Action |
| --- | --- |
| `j`/`k` | Move |
| `enter` | Checkout |
| `r` / `R` | Restack / restack `--onto-trunk` |
| `s` / `S` | Sync / sync `--onto-trunk` |
| `d` | Safe delete local branch (`git branch -d`) |
| `D` | Force-delete local branch (`git branch -D`) |
| `p` | Push (`--force-with-lease`) |
| `P` | Open/retarget PR |
| `c` | Create child (suffix prompt) |
| `f` | Fetch origin |
| `F` | Pull selected (fetch + FF-only) |
| `ctrl+r` | Refresh list + PR parents |
| `?` | Help |
| `q` | Quit |

## How parents are shared

- **Draft or ready PR** base is the team-visible stack parent (`gh pr list` bulk-loaded, cached under `.git/git-stack/`).
- **Local config** covers branches without a PR yet and offline work.
- Opening/retargeting a PR with `git-stack pr` or `reparent` keeps base in sync.

## Safety

- Tracked dirty worktree / rebase-in-progress refused (untracked OK)
- Diverged local/origin blocks the plan
- Push opt-in via `--push` (`--force-with-lease`)
- Restack cutoff prefers `git merge-base --fork-point`

## Development

```bash
go test ./...
go build -o git-stack ./cmd/git-stack
```

## License

MIT
