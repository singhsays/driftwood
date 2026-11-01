package git

import (
	"context"
	"driftwood/internal/model"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// fixture sets up a local bare remote and a Repo pointed at it.
func fixture(t *testing.T) (*Repo, string) {
	t.Helper()
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	cfg := Config{
		Remote: remote,
		Branch: "main",
		Dir:    filepath.Join(base, "mirror"),
		Name:   "driftwood",
		Email:  "driftwood@test",
		// file:// remotes need no auth; KeyFile empty is fine.
	}
	r, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r, remote
}

func headCommit(t *testing.T, bareDir string) *object.Commit {
	t.Helper()
	bare, err := git.PlainOpen(bareDir)
	if err != nil {
		t.Fatalf("open bare: %v", err)
	}
	ref, err := bare.Reference(plumbing.Main, false)
	if err != nil {
		return nil // unborn
	}
	c, err := bare.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("commit object: %v", err)
	}
	return c
}

func TestStoreCommitsOnChange(t *testing.T) {
	r, remote := fixture(t)

	// First store: unborn remote, commit + push expected.
	changed, err := r.Store("gateway", []model.Artifact{
		{Name: "config.xml", Data: []byte("<config v=1/>")},
	})
	if err != nil {
		t.Fatalf("first Store: %v", err)
	}
	if !changed {
		t.Fatal("first Store should commit")
	}
	c := headCommit(t, remote)
	if c == nil {
		t.Fatal("remote has no commits after first Store")
	}
	if c.Author.Name != "driftwood" {
		t.Errorf("author = %q", c.Author.Name)
	}

	// Re-store identical content: no new commit.
	changed, err = r.Store("gateway", []model.Artifact{
		{Name: "config.xml", Data: []byte("<config v=1/>")},
	})
	if err != nil {
		t.Fatalf("unchanged Store: %v", err)
	}
	if changed {
		t.Fatal("unchanged content should not commit")
	}
	if c2 := headCommit(t, remote); c2.Hash != c.Hash {
		t.Fatal("unchanged content created a commit")
	}

	// Change content: new commit.
	changed, err = r.Store("gateway", []model.Artifact{
		{Name: "config.xml", Data: []byte("<config v=2/>")},
	})
	if err != nil {
		t.Fatalf("changed Store: %v", err)
	}
	if !changed {
		t.Fatal("changed content should commit")
	}
	c3 := headCommit(t, remote)
	if c3.Hash == c.Hash {
		t.Fatal("changed content did not create a new commit")
	}
	if got := c3.Message; got != "gateway: 1 file changed" {
		t.Errorf("commit message = %q", got)
	}
}

func TestStoreNodeLayout(t *testing.T) {
	r, remote := fixture(t)
	if _, err := r.Store("elanor", []model.Artifact{
		{Name: "modprobe/hba-blacklist.conf", Data: []byte("blacklist mpt3sas\n")},
		{Name: "interfaces", Data: []byte("auto lo\n")},
	}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	// Files land in the mirror under nodes/<node>/ with nested paths.
	for _, p := range []string{"nodes/elanor/modprobe/hba-blacklist.conf", "nodes/elanor/interfaces"} {
		if _, err := os.Stat(filepath.Join(r.cfg.Dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	if headCommit(t, remote) == nil {
		t.Fatal("no commit pushed")
	}
}
