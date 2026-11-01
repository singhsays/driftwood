package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	yaml := `
defaults:
  user: root
  interval: 1h
  keyfile: ~/.ssh/id_ed25519
nodes:
  - name: a
    model: generic
    host: a.lan
  - name: b
    model: routeros
    host: b.lan
    interval: 10m
`
	cfg, err := Load(yamlStr(t, yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(cfg.Nodes))
	}
	a := cfg.Nodes[0]
	if a.User != "root" || a.Interval != "1h" || a.Port != 22 {
		t.Errorf("defaults not applied to node a: %+v", a)
	}
	if a.IntervalOf() == 0 {
		t.Errorf("interval not parseable for node a")
	}
	b := cfg.Nodes[1]
	if b.Interval != "10m" || b.User != "root" {
		t.Errorf("per-node override lost on node b: %+v", b)
	}
}

func TestLoadValidation(t *testing.T) {
	cases := map[string]string{
		"missing model":    "nodes:\n  - name: a\n    host: a.lan\n",
		"missing host":     "nodes:\n  - name: a\n    model: routeros\n",
		"duplicate name":   "nodes:\n  - name: a\n    model: routeros\n    host: a.lan\n  - name: a\n    model: routeros\n    host: b.lan\n",
		"bad interval":     "nodes:\n  - name: a\n    model: routeros\n    host: a.lan\n    interval: forever\n",
		"defaults bad int": "defaults:\n  interval: someday\nnodes:\n  - name: a\n    model: routeros\n    host: a.lan\n",
	}
	for name, yaml := range cases {
		if _, err := Load(yamlStr(t, yaml)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestLoadNameDefaultsToHost(t *testing.T) {
	cfg, err := Load(yamlStr(t, "nodes:\n  - model: routeros\n    host: sw.lan\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Nodes[0].Name != "sw.lan" {
		t.Errorf("name should default to host, got %q", cfg.Nodes[0].Name)
	}
}

// yamlStr writes YAML to a temp file and returns its path, since Load
// takes a path.
func yamlStr(t *testing.T, y string) string {
	t.Helper()
	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, []byte(y), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
