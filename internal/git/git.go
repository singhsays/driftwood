// Package git implements git operations for repository outputs.
package git

import (
	"context"
	"driftwood/internal/model"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"

	dwconfig "driftwood/internal/config"
)

// Config is the git specific configuration.
type Config = dwconfig.Git

// nodesDir is the top-level layout of the output repo.
const nodesDir = "nodes"

// Repo is a local mirror of the output remote.
type Repo struct {
	cfg  Config
	auth transport.AuthMethod
	repo *git.Repository
	wt   *git.Worktree
	mu   sync.Mutex // serializes Store: go-git worktrees are not concurrent-safe
}

// Open clones cfg.Remote into cfg.Dir (first run) or opens the existing
// local mirror, then fast-forwards it to the remote branch. The local
// mirror is disposable — anything divergent is hard-reset.
func Open(ctx context.Context, cfg Config) (*Repo, error) {
	if cfg.Remote == "" {
		return nil, errors.New("git: remote is required")
	}
	if cfg.Branch == "" {
		cfg.Branch = "main"
	}
	if cfg.Dir == "" {
		return nil, errors.New("git: dir is required for the local mirror")
	}
	auth, err := buildAuth(cfg.Remote, cfg.KeyFile)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Dir), 0o750); err != nil {
		return nil, fmt.Errorf("git: create parent dir: %w", err)
	}

	var repo *git.Repository
	if _, serr := os.Stat(filepath.Join(cfg.Dir, ".git")); errors.Is(serr, fs.ErrNotExist) {
		repo, err = cloneOrInit(ctx, cfg, auth)
		if err != nil {
			return nil, err
		}
	} else {
		repo, err = git.PlainOpen(cfg.Dir)
		if err != nil {
			return nil, fmt.Errorf("git: open %s: %w", cfg.Dir, err)
		}
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("git: worktree: %w", err)
	}
	r := &Repo{cfg: cfg, auth: auth, repo: repo, wt: wt}
	if err := r.sync(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// cloneOrInit clones the remote; if the remote is empty (fresh output
// repo), it initializes a local repo pointing at the remote instead.
func cloneOrInit(ctx context.Context, cfg Config, auth transport.AuthMethod) (*git.Repository, error) {
	repo, err := git.PlainCloneContext(ctx, cfg.Dir, false, &git.CloneOptions{
		URL:        cfg.Remote,
		Auth:       auth,
		RemoteName: "origin",
	})
	if err == nil {
		return repo, nil
	}
	// Empty remotes are expected for a brand-new output repo.
	if !errors.Is(err, transport.ErrEmptyRemoteRepository) && !errors.Is(err, git.ErrRepositoryNotExists) {
		// Some go-git versions wrap the empty-remote case in a generic
		// transport error; fall back to init only when the local dir is
		// still absent, so a genuine clone failure isn't masked.
		if _, serr := os.Stat(filepath.Join(cfg.Dir, ".git")); serr != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "empty") {
			return nil, fmt.Errorf("git: clone: %w", err)
		}
	}
	_ = os.RemoveAll(cfg.Dir)
	repo, err = git.PlainInit(cfg.Dir, false)
	if err != nil {
		return nil, fmt.Errorf("git: init: %w", err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{cfg.Remote},
	}); err != nil {
		return nil, fmt.Errorf("git: remote: %w", err)
	}
	return repo, nil
}

// sync fetches origin and hard-resets the worktree to the remote branch,
// keeping the disposable mirror in lockstep. A repo without commits
// (fresh output repo) is left unborn.
func (r *Repo) sync(ctx context.Context) error {
	err := r.repo.FetchContext(ctx, &git.FetchOptions{
		Auth:       r.auth,
		RemoteName: "origin",
		Force:      true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) && !errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return fmt.Errorf("git: fetch: %w", err)
	}
	ref, err := r.repo.Reference(plumbing.NewRemoteReferenceName("origin", r.cfg.Branch), false)
	if err != nil {
		// No remote branch yet: unborn repo. Make sure HEAD points at the
		// configured branch — PlainInit defaults to "master" while the
		// push refspec targets cfg.Branch.
		if _, herr := r.repo.Head(); herr != nil && errors.Is(herr, plumbing.ErrReferenceNotFound) {
			if serr := r.repo.Storer.SetReference(plumbing.NewSymbolicReference(
				plumbing.HEAD, plumbing.NewBranchReferenceName(r.cfg.Branch))); serr != nil {
				return fmt.Errorf("git: set HEAD to %s: %w", r.cfg.Branch, serr)
			}
		}
		return nil
	}
	branchRef := plumbing.NewBranchReferenceName(r.cfg.Branch)
	if _, err := r.repo.Reference(branchRef, false); err != nil {
		// Local branch doesn't exist yet: point it at the remote head.
		if err := r.wt.Checkout(&git.CheckoutOptions{
			Branch: branchRef,
			Hash:   ref.Hash(),
			Create: true,
			Force:  true,
		}); err != nil {
			return fmt.Errorf("git: checkout %s: %w", r.cfg.Branch, err)
		}
		return nil
	}
	if err := r.wt.Reset(&git.ResetOptions{
		Mode:   git.HardReset,
		Commit: ref.Hash(),
	}); err != nil {
		return fmt.Errorf("git: reset to origin/%s: %w", r.cfg.Branch, err)
	}
	return nil
}

// Store writes one node's artifacts under <nodesDir>/<node>/, commits when
// content changed, and pushes. It reports whether a commit was made.
// Serialized: go-git worktrees are not safe for concurrent use, and the
// daemon fetches nodes concurrently.
func (r *Repo) Store(node string, arts []model.Artifact) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range arts {
		path := filepath.Join(nodesDir, node, a.Name)
		if err := os.MkdirAll(filepath.Join(r.cfg.Dir, filepath.Dir(path)), 0o750); err != nil {
			return false, fmt.Errorf("git: mkdir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(r.cfg.Dir, path), a.Data, 0o600); err != nil {
			return false, fmt.Errorf("git: write %s: %w", path, err)
		}
		if _, err := r.wt.Add(path); err != nil {
			return false, fmt.Errorf("git: add %s: %w", path, err)
		}
	}

	status, err := r.wt.Status()
	if err != nil {
		return false, fmt.Errorf("git: status: %w", err)
	}
	if status.IsClean() {
		return false, nil
	}
	changed := len(status)
	msg := fmt.Sprintf("%s: %d file%s changed", node, changed, plural(changed))
	if err := r.commit(msg); err != nil {
		return false, err
	}
	if err := r.push(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repo) commit(msg string) error {
	sig := object.Signature{
		Name:  r.cfg.Name,
		Email: r.cfg.Email,
		When:  time.Now(),
	}
	_, err := r.wt.Commit(msg, &git.CommitOptions{
		Author: &sig,
		// The mirror is disposable; commit straight onto the branch head
		// regardless of worktree HEAD oddities.
		All: true,
	})
	if err != nil {
		return fmt.Errorf("git: commit: %w", err)
	}
	return nil
}

func (r *Repo) push() error {
	spec := config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", r.cfg.Branch, r.cfg.Branch))
	if err := r.repo.Push(&git.PushOptions{
		RemoteName: "origin",
		Auth:       r.auth,
		RefSpecs:   []config.RefSpec{spec},
	}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("git: push: %w", err)
	}
	return nil
}

// buildAuth returns SSH key auth for SSH remotes, nil for HTTP(S) and
// local file remotes, and an error when an SSH remote has no key
// configured.
func buildAuth(remote, keyFile string) (transport.AuthMethod, error) {
	isSSH := strings.HasPrefix(remote, "ssh://") ||
		strings.HasPrefix(remote, "git@") ||
		strings.HasPrefix(remote, "user@")
	if !isSSH {
		// HTTP(S) and local file remotes need no key auth.
		return nil, nil
	}
	if keyFile == "" {
		return nil, fmt.Errorf("git: keyfile is required for ssh remote %s", remote)
	}
	keys, err := gitssh.NewPublicKeysFromFile("git", keyFile, "")
	if err != nil {
		return nil, fmt.Errorf("git: load key %s: %w", keyFile, err)
	}
	// Verify server host keys against the standard known_hosts files
	// (SSH_KNOWN_HOSTS, ~/.ssh/known_hosts, /etc/ssh/ssh_known_hosts).
	cb, err := gitssh.NewKnownHostsCallback()
	if err != nil {
		return nil, fmt.Errorf(
			"git: no known_hosts entries found — SSH to the remote host once to record its key: %w", err)
	}
	keys.HostKeyCallback = cb
	return keys, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
