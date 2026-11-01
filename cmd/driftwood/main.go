// Command driftwood turns device configuration drift into git history.
package main

import (
	"context"
	"driftwood/internal/config"
	"driftwood/internal/runner"
	"driftwood/internal/version"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
)

// errFetchFailed is returned from Run when any node failed; details are
// already logged by PrintResults.
var errFetchFailed = errors.New("one or more nodes failed")

// CLI is the driftwood command line.
type CLI struct {
	Run     RunCmd     `cmd:"" help:"Fetch all nodes once and exit."`
	Daemon  DaemonCmd  `cmd:"" help:"Fetch continuously until interrupted."`
	Version VersionCmd `cmd:"" help:"Print version."`
}

// RunCmd fetches every node once, then exits.
type RunCmd struct {
	Config string   `short:"c" help:"Path to config file." default:"config.yaml"`
	Nodes  []string `name:"node" help:"Restrict to node(s) by name (repeatable)."`
}

// Run executes the fetch.
func (r *RunCmd) Run() error {
	cfg, err := config.Load(r.Config)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := filterNodes(cfg, r.Nodes); err != nil {
		return err
	}
	results := runner.Once(context.Background(), cfg)
	runner.PrintResults(results)
	for _, res := range results {
		if res.Err != nil {
			return errFetchFailed
		}
	}
	return nil
}

// DaemonCmd fetches each node on its interval until interrupted.
type DaemonCmd struct {
	Config string `short:"c" help:"Path to config file." default:"config.yaml"`
}

// Run starts the daemon loop.
func (d *DaemonCmd) Run() error {
	cfg, err := config.Load(d.Config)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runner.Daemon(ctx, cfg)
}

// VersionCmd prints the version.
type VersionCmd struct{}

// Run prints the version.
func (v *VersionCmd) Run() error { //nolint:unparam // kong requires the error return even though it is always nil
	fmt.Println("driftwood", version.Full())
	return nil
}

// filterNodes restricts cfg.Nodes to the named ones, validating names.
func filterNodes(cfg *config.Config, names []string) error {
	if len(names) == 0 {
		return nil
	}
	byName := map[string]config.Node{}
	for _, n := range cfg.Nodes {
		byName[n.Name] = n
	}
	selected := make([]config.Node, 0, len(names))
	for _, name := range names {
		n, ok := byName[name]
		if !ok {
			return fmt.Errorf("unknown node %q", name)
		}
		selected = append(selected, n)
	}
	cfg.Nodes = selected
	return nil
}

func main() {
	log.SetFlags(log.Ltime)

	cli := &CLI{}
	ctx := kong.Parse(cli,
		kong.Name("driftwood"),
		kong.Description("Turn config drift into git history."),
		kong.UsageOnError(),
	)
	if err := ctx.Run(); err != nil {
		log.Fatalf("%v", err)
	}
}
