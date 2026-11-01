package main

import (
	"driftwood/internal/config"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

func TestFilterNodes(t *testing.T) {
	cfg := &config.Config{Nodes: []config.Node{
		{Name: "a", Host: "a.lan"},
		{Name: "b", Host: "b.lan"},
	}}

	// No filter: unchanged.
	if err := filterNodes(cfg, nil); err != nil {
		t.Fatalf("filterNodes(nil): %v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(cfg.Nodes))
	}

	// Filter to one.
	if err := filterNodes(cfg, []string{"b"}); err != nil {
		t.Fatalf("filterNodes(b): %v", err)
	}
	if len(cfg.Nodes) != 1 || cfg.Nodes[0].Name != "b" {
		t.Fatalf("unexpected selection: %+v", cfg.Nodes)
	}

	// Unknown name errors.
	err := filterNodes(&config.Config{Nodes: cfg.Nodes}, []string{"zzz"})
	if err == nil || !strings.Contains(err.Error(), `unknown node "zzz"`) {
		t.Fatalf("filterNodes(zzz) = %v, want unknown-node error", err)
	}
}

// TestVersionDispatch runs the version subcommand end-to-end through kong.
func TestVersionDispatch(t *testing.T) {
	k, err := kong.New(&CLI{}, kong.Name("driftwood"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatalf("parser: %v", err)
	}
	ctx, err := k.Parse([]string{"version"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ctx.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
}
