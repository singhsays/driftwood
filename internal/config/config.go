// Package config loads and validates the driftwood YAML configuration.
package config

import (
	"driftwood/internal/paths"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Git holds the output repository settings.
type Git struct {
	Remote string `yaml:"remote"`
	Branch string `yaml:"branch"`
	// KeyFile is the deploy key used to push to Remote.
	KeyFile string `yaml:"keyfile"`
	// Dir is the local mirror of Remote (clone lives here).
	Dir string `yaml:"dir"`
	// Author for commits made by driftwood.
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// Node is one managed device.
type Node struct {
	Name string `yaml:"name"`
	// Model is the built-in model key (routeros, pfsense, proxmox, ...).
	Model string `yaml:"model"`
	Host  string `yaml:"host"`
	User  string `yaml:"user"`
	Port  int    `yaml:"port"`
	// Interval between fetches, e.g. 24h.
	Interval string `yaml:"interval"`
	// KeyFile overrides the SSH private key for this node.
	KeyFile string `yaml:"keyfile"`
	// Commands supplies per-node commands for the generic exec model.
	Commands []string `yaml:"commands"`
	// Files supplies per-node file paths for the generic file model.
	Files []string `yaml:"files"`
	// Encrypt encrypts this node's artifacts to the SSH public key at
	// the given path before they are stored (age SSH recipient).
	Encrypt string `yaml:"encrypt"`
}

// Default values applied when the config omits them.
const (
	defaultPort     = 22
	defaultInterval = "24h"
)

// Config is the driftwood configuration file.
type Config struct {
	Git   Git    `yaml:"git"`
	Nodes []Node `yaml:"nodes"`

	// Parsed defaults.
	Defaults Defaults `yaml:"defaults"`
}

// Defaults applied to nodes that omit fields.
type Defaults struct {
	User     string `yaml:"user"`
	Port     int    `yaml:"port"`
	Interval string `yaml:"interval"`
	KeyFile  string `yaml:"keyfile"`
}

// Load reads and validates a YAML config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() error {
	if c.Defaults.Port == 0 {
		c.Defaults.Port = defaultPort
	}
	if c.Defaults.Interval == "" {
		c.Defaults.Interval = defaultInterval
	}
	// An empty user means "the current user"; in a homelab that is rarely
	// intended, so warn rather than silently mis-authenticate.
	if c.Defaults.User == "" {
		c.Defaults.User = "root"
	}
	seen := map[string]bool{}
	for i := range c.Nodes {
		n := &c.Nodes[i]
		if n.Name == "" {
			n.Name = n.Host
		}
		if seen[n.Name] {
			return fmt.Errorf("duplicate node name %q", n.Name)
		}
		seen[n.Name] = true
		if n.Model == "" {
			return fmt.Errorf("node %s: model is required", n.Name)
		}
		if n.Host == "" {
			return fmt.Errorf("node %s: host is required", n.Name)
		}
		if n.User == "" {
			n.User = c.Defaults.User
		}
		if n.Port == 0 {
			n.Port = c.Defaults.Port
		}
		if n.Interval == "" {
			n.Interval = c.Defaults.Interval
		}
		if _, err := time.ParseDuration(n.Interval); err != nil {
			return fmt.Errorf("node %s: bad interval %q: %w", n.Name, n.Interval, err)
		}
		if n.KeyFile == "" {
			n.KeyFile = c.Defaults.KeyFile
		}
		if n.Encrypt != "" {
			p := paths.ExpandHome(n.Encrypt)
			if _, err := os.Stat(p); err != nil {
				return fmt.Errorf("node %s: encrypt key not found: %w", n.Name, err)
			}
			n.Encrypt = p
		}
	}
	return nil
}

// IntervalOf returns the parsed interval for a node.
func (n *Node) IntervalOf() time.Duration {
	d, _ := time.ParseDuration(n.Interval)
	return d
}
