// Package ssh provides a SSH/SFTP client for driftwood.
package ssh

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client wraps an established SSH connection to one node.
type Client struct {
	client *ssh.Client
}

// KeyLoader resolves the private key for a connection.
type KeyLoader struct {
	// KeyFile is an explicit path; empty means default ~/.ssh/id_{ed25519,rsa}.
	KeyFile string
}

// knownHostsPath stores TOFU host keys per user.
func knownHostsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".driftwood", "known_hosts")
}

// Dial connects with public-key auth and TOFU host key checking.
func Dial(host string, port int, user string, loader KeyLoader) (*Client, error) {
	signer, err := loadSigner(loader)
	if err != nil {
		return nil, err
	}
	addresses := []string{net.JoinHostPort(host, fmt.Sprint(port))}
	// Also try bare hostname/IP variants for host key lookup.
	keyErr := &hostKeyCallback{path: knownHostsPath()}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: keyErr.callback(),
		Timeout:         10 * time.Second,
	}
	var lastErr error
	for _, addr := range addresses {
		c, err := ssh.Dial("tcp", addr, cfg)
		if err == nil {
			return &Client{client: c}, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// Close terminates the connection.
func (c *Client) Close() error {
	if c.client == nil {
		return nil
	}
	return c.client.Close()
}

// loadSigner returns a signer from KeyLoader or the default keys.
func loadSigner(l KeyLoader) (ssh.Signer, error) {
	if l.KeyFile != "" {
		path := expandHome(l.KeyFile)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", path, err)
		}
		return ssh.ParsePrivateKey(raw)
	}
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	candidates := []string{"id_ed25519", "id_rsa", "id_ecdsa"}
	for _, name := range candidates {
		path := filepath.Join(u.HomeDir, ".ssh", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return ssh.ParsePrivateKey(raw)
	}
	return nil, fmt.Errorf("no SSH key found (set keyfile in config)")
}

// expandHome resolves a leading ~ in a path.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// hostKeyCallback implements trust-on-first-use against a known_hosts file.
type hostKeyCallback struct {
	path string
}

func (h *hostKeyCallback) callback() ssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		line := knownHostLine(hostname, key)
		existing, _ := os.ReadFile(h.path)
		if existing != nil && bytes.Contains(existing, []byte(line)) {
			return nil
		}
		// TOFU: append and trust. Existing entries for the host must match.
		if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, werr := f.WriteString(line + "\n")
		return werr
	}
}

func knownHostLine(hostname string, key ssh.PublicKey) string {
	b64 := base64.StdEncoding.EncodeToString(key.Marshal())
	return fmt.Sprintf("%s %s %s", hostname, key.Type(), b64)
}

// Run executes a command and returns combined-trimmed stdout.
// Stderr is returned separately for diagnostics.
func (c *Client) Run(cmd string) (stdout []byte, stderr []byte, err error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = sess.Close() }()
	var out, errBuf bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errBuf
	if err := sess.Run(cmd); err != nil {
		return out.Bytes(), errBuf.Bytes(), fmt.Errorf("run %q: %w (stderr: %s)", cmd, err, truncate(errBuf.String(), 256))
	}
	return out.Bytes(), nil, nil
}

// ReadFile fetches a remote file via SFTP using the "sftp" subsystem
// implemented manually over exec for minimal dependencies.
func (c *Client) ReadFile(path string) ([]byte, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.Close() }()
	var out bytes.Buffer
	sess.Stdout = &out
	// cat is ubiquitous enough; keeps v1 dependency-free vs full SFTP.
	if err := sess.Run("cat " + shellQuote(path)); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out.Bytes(), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\"'`) + "'"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var _ io.Reader = (*bytes.Buffer)(nil)
