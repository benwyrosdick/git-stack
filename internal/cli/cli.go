// Package cli implements git-stack subcommands.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/benwyrosdick/git-stack/internal/gh"
	"github.com/benwyrosdick/git-stack/internal/git"
	"github.com/benwyrosdick/git-stack/internal/stack"
	"github.com/benwyrosdick/git-stack/internal/tui"
)

// Version is set via -ldflags at release time.
var Version = "dev"

// Run parses args and executes. args should be os.Args[1:].
func Run(args []string) error {
	if len(args) == 0 {
		return runTUI(false, false)
	}
	// Global flags may appear before subcommand.
	offline, refresh := false, false
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--offline":
			offline = true
		case "--refresh":
			refresh = true
		default:
			filtered = append(filtered, a)
		}
	}
	args = filtered
	if len(args) == 0 {
		return runTUI(offline, refresh)
	}
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "help", "-h", "--help":
		printHelp(os.Stdout)
		return nil
	case "version", "--version", "-v":
		fmt.Println(Version)
		return nil
	case "parent":
		return cmdParent(rest, offline, refresh)
	case "create":
		return cmdCreate(rest, offline, refresh)
	case "restack":
		return cmdRestack(rest, offline, refresh)
	case "reparent":
		return cmdReparent(rest, offline, refresh)
	case "sync":
		return cmdSync(rest, offline, refresh)
	case "pr":
		return cmdPR(rest, offline, refresh)
	case "ls", "list":
		return cmdLS(rest, offline, refresh)
	case "track":
		return cmdTrack(rest, offline, refresh)
	case "untrack":
		return cmdUntrack(rest)
	case "adopt":
		return cmdAdopt(rest, offline, refresh)
	case "tui":
		return runTUI(offline, refresh)
	default:
		return fmt.Errorf("unknown command: %s (try: git-stack help)", cmd)
	}
}

func engine(offline, refresh bool) (*stack.Engine, *git.Repo, error) {
	repo := &git.Repo{}
	if err := repo.RequireRepo(); err != nil {
		return nil, nil, err
	}
	eng := &stack.Engine{Repo: repo, Out: os.Stderr}
	_ = eng.LoadParents(stack.LoadParentsOpts{Offline: offline, Refresh: refresh})
	return eng, repo, nil
}

func cmdParent(args []string, offline, refresh bool) error {
	eng, _, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	branch := ""
	verbose := false
	for _, a := range args {
		switch a {
		case "-v", "--verbose":
			verbose = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("parent: unknown flag %s", a)
			}
			branch = a
		}
	}
	if branch == "" {
		branch, err = eng.Repo.CurrentBranch()
		if err != nil {
			return err
		}
	}
	p, src := eng.ParentOfWithSource(branch)
	if verbose {
		fmt.Printf("%s  (%s)\n", p, src)
	} else {
		fmt.Println(p)
	}
	return nil
}

func cmdCreate(args []string, offline, refresh bool) error {
	eng, _, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	opts := stack.CreateOpts{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from", "--start", "--parent":
			if i+1 >= len(args) {
				return fmt.Errorf("create: %s requires an argument", args[i])
			}
			opts.From = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("create: unknown flag %s", args[i])
			}
			if opts.Name != "" {
				return fmt.Errorf("create: unexpected argument %s", args[i])
			}
			opts.Name = args[i]
		}
	}
	return eng.Create(opts)
}

func cmdRestack(args []string, offline, refresh bool) error {
	eng, _, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	opts := stack.RestackOpts{}
	for _, a := range args {
		switch a {
		case "--push":
			opts.Push = true
		case "--onto-trunk":
			opts.OntoTrunk = true
		case "--no-fetch":
			opts.NoFetch = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("restack: unknown flag %s", a)
			}
			if opts.Branch != "" {
				return fmt.Errorf("restack: unexpected argument %s", a)
			}
			opts.Branch = a
		}
	}
	return eng.Restack(opts)
}

func cmdReparent(args []string, offline, refresh bool) error {
	eng, repo, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	opts := stack.ReparentOpts{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 >= len(args) {
				return fmt.Errorf("reparent: --from requires an argument")
			}
			opts.OldParent = args[i+1]
			i++
		case "--push":
			opts.Push = true
		case "--no-fetch":
			opts.NoFetch = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("reparent: unknown flag %s", args[i])
			}
			if opts.Branch == "" {
				opts.Branch = args[i]
			} else if opts.NewParent == "" {
				opts.NewParent = args[i]
			} else {
				return fmt.Errorf("reparent: unexpected argument %s", args[i])
			}
		}
	}
	if err := eng.Reparent(opts); err != nil {
		return err
	}
	if gh.Available() {
		_ = gh.RetargetBase(repo, opts.Branch, opts.NewParent)
	}
	return nil
}

func cmdSync(args []string, offline, refresh bool) error {
	eng, _, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	opts := stack.SyncOpts{}
	for _, a := range args {
		switch a {
		case "--push":
			opts.Push = true
		case "--onto-trunk":
			opts.OntoTrunk = true
		case "--dry-run":
			opts.DryRun = true
		case "--no-fetch":
			opts.NoFetch = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("sync: unknown flag %s", a)
			}
			if opts.Root != "" {
				return fmt.Errorf("sync: unexpected argument %s", a)
			}
			opts.Root = a
		}
	}
	_, err = eng.Sync(opts)
	return err
}

func cmdPR(args []string, offline, refresh bool) error {
	eng, repo, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	opts := gh.PROpts{}
	for _, a := range args {
		switch a {
		case "--draft":
			opts.Draft = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("pr: unknown flag %s", a)
			}
			if opts.Branch != "" {
				return fmt.Errorf("pr: unexpected argument %s", a)
			}
			opts.Branch = a
		}
	}
	branch := opts.Branch
	if branch == "" {
		branch, err = repo.CurrentBranch()
		if err != nil {
			return err
		}
		opts.Branch = branch
	}
	opts.Base = eng.ParentOf(branch)
	url, err := gh.EnsurePR(repo, opts)
	if err != nil {
		return err
	}
	eng.InvalidateParentCache()
	_ = eng.LoadParents(stack.LoadParentsOpts{Refresh: true})
	if url != "" {
		fmt.Println(url)
	}
	return nil
}

func cmdLS(args []string, offline, refresh bool) error {
	eng, _, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	root := ""
	if len(args) > 0 {
		root = args[0]
	}
	infos, err := eng.List(root)
	if err != nil {
		return err
	}
	fmt.Print(stack.FormatList(root, infos))
	return nil
}

func cmdTrack(args []string, offline, refresh bool) error {
	eng, _, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	branch, parent := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--parent", "--from":
			if i+1 >= len(args) {
				return fmt.Errorf("track: %s requires an argument", args[i])
			}
			parent = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("track: unknown flag %s", args[i])
			}
			if branch == "" {
				branch = args[i]
			} else if parent == "" {
				parent = args[i]
			} else {
				return fmt.Errorf("track: unexpected argument %s", args[i])
			}
		}
	}
	if parent == "" {
		return fmt.Errorf("usage: git-stack track <branch> --parent <parent>")
	}
	return eng.Track(branch, parent)
}

func cmdUntrack(args []string) error {
	eng, _, err := engine(true, false)
	if err != nil {
		return err
	}
	branch := ""
	if len(args) > 0 {
		branch = args[0]
	}
	return eng.Untrack(branch)
}

func cmdAdopt(args []string, offline, refresh bool) error {
	eng, _, err := engine(offline, refresh)
	if err != nil {
		return err
	}
	root := ""
	if len(args) > 0 {
		root = args[0]
	}
	return eng.Adopt(root)
}

func runTUI(offline, refresh bool) error {
	repo := &git.Repo{}
	if err := repo.RequireRepo(); err != nil {
		return err
	}
	return tui.Run(repo, offline, refresh)
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `git-stack — stacked branches via PR base, local config, or dot names

  (no args)                                Interactive TUI
  parent  [branch] [-v]                    Print parent ( -v shows source: pr|local|name)
  create  <name> [--from <start>]          Create branch; records parent metadata
  restack [branch] [--push] [--onto-trunk] Replay branch onto parent
          [--no-fetch]
  reparent <branch> <new-parent>           Rebase + update parent metadata / PR base
           [--from <old-parent>] [--push] [--no-fetch]
  sync    [root] [--push] [--onto-trunk]   Fix root then descendants
           [--dry-run] [--no-fetch]
  pr      [branch] [--draft]               Create/retarget PR (base = stack parent)
  track   <branch> --parent <parent>       Set local parent only (no rebase)
  untrack [branch]                         Clear local parent metadata
  adopt   [root]                           Write local parents from current resolution
  ls      [root]                           List stack tree
  tui                                      Open interactive TUI
  version                                  Print version

Global flags:
  --offline     Do not call gh; use local config + cache + names
  --refresh     Force refresh of open-PR parent map

Parent resolution (first match wins):
  1. Open PR base (shared with team; draft OK)
  2. Local git config branch.<name>.gitstack-parent
  3. Dot-depth name (feature.ui → feature)
  4. Default branch (trunk)

Examples:
  git-stack create api-work
  git-stack create ui-work --from api-work    # free names, no dots required
  git-stack pr --draft                       # PR base becomes team-shared parent
  git-stack reparent ui-work other-api       # move in/out of stacks without rename
  git-stack sync api-work --dry-run          # preview plan only
`)
}
