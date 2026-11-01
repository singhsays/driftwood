// Package model defines fetch models
// models specify how to pull config artifacts from a device.
package model

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// Naming conventions for generated artifact names.
const (
	// globMetaChars are the shell metacharacters that make a Files entry
	// a glob pattern, expanded remotely before fetching.
	globMetaChars = "*?["

	// commandArtifactPrefix and fileArtifactPrefix name artifacts when a
	// spec does not provide an explicit name.
	commandArtifactPrefix = "cmd"
	fileArtifactPrefix    = "file"

	// textArtifactExt is appended to generated artifact names.
	textArtifactExt = ".txt"

	// fallbackArtifactName is used when a command string yields no usable
	// name characters.
	fallbackArtifactName = "command"

	// maxNameRunes bounds the length of names derived from commands.
	maxNameRunes = 40
)

// Artifact is one fetched piece of a node's configuration.
type Artifact struct {
	// Name is the filename used in the git output tree.
	Name string
	Data []byte
	// Binary artifacts are hash-compared and not diffed.
	Binary bool
}

// Client is the transport surface models fetch through.
// *ssh.Client satisfies it; tests use fakes.
type Client interface {
	// Run executes a command over the connection.
	Run(cmd string) (stdout []byte, stderr []byte, err error)
	// ReadFile fetches a file's contents.
	ReadFile(path string) ([]byte, error)
	// Expand resolves a glob pattern to matching remote paths.
	Expand(pattern string) ([]string, error)
}

// Fetcher fetches all artifacts for a node.
type Fetcher interface {
	Fetch(c Client) ([]Artifact, error)
}

// Spec describes a model's fetch behavior declaratively.
type Spec struct {
	// Commands to run over SSH, in order.
	Commands []string
	// Files to fetch, in order.
	Files []string
	// Strip removes matching lines from command output (e.g. prompts).
	Strip []*regexp.Regexp
	// Artifact names, matching Commands/Files by index. Empty means generated.
	CommandNames []string
	FileNames    []string
	// Binary marks all outputs as binary (hash-only in git output).
	Binary bool
	// SkipMissing silently skips Files that do not exist on the remote
	// (e.g. corosync.conf on a non-clustered Proxmox host). Other read
	// errors still fail the fetch.
	SkipMissing bool
}

// Compile turns a Spec into a Fetcher.
func (s Spec) Compile() Fetcher { return &specFetcher{spec: s} }

type specFetcher struct{ spec Spec }

func (f *specFetcher) Fetch(c Client) ([]Artifact, error) {
	var out []Artifact
	for i, cmd := range f.spec.Commands {
		data, _, err := c.Run(cmd)
		if err != nil {
			return nil, fmt.Errorf("command %d: %w", i, err)
		}
		data = stripLines(string(data), f.spec.Strip)
		out = append(out, Artifact{
			Name:   specName(f.spec.CommandNames, i, commandArtifactPrefix, i),
			Data:   data,
			Binary: f.spec.Binary,
		})
	}
	for i, path := range f.spec.Files {
		if hasGlobMeta(path) {
			matches, err := c.Expand(path)
			if err != nil {
				return nil, fmt.Errorf("glob %s: %w", path, err)
			}
			// An empty match set is not an error — a glob with no matches
			// simply produces no artifacts.
			base := filepath.Dir(path)
			for _, m := range matches {
				data, err := c.ReadFile(m)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) && f.spec.SkipMissing {
						continue
					}
					return nil, fmt.Errorf("file %s: %w", m, err)
				}
				out = append(out, Artifact{
					// Named relative to the pattern's directory; slashes
					// become subdirectories in the git output tree (M2).
					Name:   relName(base, m),
					Data:   data,
					Binary: f.spec.Binary,
				})
			}
			continue
		}
		data, err := c.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && f.spec.SkipMissing {
				continue
			}
			return nil, fmt.Errorf("file %s: %w", path, err)
		}
		out = append(out, Artifact{
			Name:   specName(f.spec.FileNames, i, fileArtifactPrefix, i),
			Data:   data,
			Binary: f.spec.Binary,
		})
	}
	return out, nil
}

// hasGlobMeta reports whether a path contains glob metacharacters.
func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, globMetaChars)
}

// relName names a glob match relative to the pattern's directory.
func relName(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." {
		return filepath.Base(path)
	}
	return rel
}

func specName(names []string, i int, prefix string, n int) string {
	if i < len(names) && names[i] != "" {
		return names[i]
	}
	return fmt.Sprintf("%s-%d%s", prefix, n, textArtifactExt)
}

func stripLines(s string, rs []*regexp.Regexp) []byte {
	var kept []string
	for line := range strings.SplitSeq(strings.TrimRight(s, "\n"), "\n") {
		drop := false
		for _, r := range rs {
			if r.MatchString(line) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, line)
		}
	}
	return []byte(strings.Join(kept, "\n") + "\n")
}

// Registry of built-in models.
var registry = map[string]Spec{}

// Register adds a model spec to the registry (used by init and tests).
func Register(name string, s Spec) { registry[name] = s }

// Get returns the fetcher for a model name.
func Get(name string) (Fetcher, error) {
	spec, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown model %q (known: %v)", name, Keys())
	}
	return spec.Compile(), nil
}

// Keys lists registered model names.
func Keys() []string {
	var ks []string
	for k := range registry {
		ks = append(ks, k)
	}
	return ks
}
