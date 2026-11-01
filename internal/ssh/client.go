// Package ssh provides a SSH/SFTP client for driftwood.
package ssh

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Connection and file-handling constants.
const (
	// dialTimeout bounds the TCP connect + SSH handshake per node.
	dialTimeout = 10 * time.Second

	// knownHostsDir is where driftwood records TOFU host keys.
	knownHostsDir = ".driftwood"
	// knownHostsFile is the TOFU known_hosts file name.
	knownHostsFile = "known_hosts"

	// knownHostsDirPerms and knownHostsFilePerms keep TOFU state private.
	knownHostsDirPerms  = 0o700
	knownHostsFilePerms = 0o600

	// stderrSnippetLen bounds the stderr excerpt embedded in exec errors.
	stderrSnippetLen = 256
)

// Client wraps an established SSH connection to one node.
// File operations use the SFTP subsystem.
type Client struct {
	host   string
	client *ssh.Client
	sftp   *sftp.Client
}

// sftpClient lazily opens the SFTP subsystem.
func (c *Client) sftpClient() (*sftp.Client, error) {
	if c.sftp != nil {
		return c.sftp, nil
	}
	sc, err := sftp.NewClient(c.client)
	if err != nil {
		return nil, fmt.Errorf(
			"host %s: SFTP subsystem unavailable (%w) — devices without SFTP can only use command-based models",
			c.host, err)
	}
	c.sftp = sc
	return sc, nil
}

// defaultKeyFiles are tried, in order, when no keyfile is configured.
var defaultKeyFiles = []string{"id_ed25519", "id_rsa", "id_ecdsa"}

// KeyLoader resolves the private key for a connection.
type KeyLoader struct {
	// KeyFile is an explicit path; empty means default ~/.ssh/id_{ed25519,rsa}.
	KeyFile string
}

// knownHostsPath stores TOFU host keys per user.
func knownHostsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, knownHostsDir, knownHostsFile)
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
		Timeout:         dialTimeout,
	}
	var lastErr error
	for _, addr := range addresses {
		c, err := ssh.Dial("tcp", addr, cfg)
		if err == nil {
			return &Client{host: host, client: c}, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// Close terminates the connection and any open subsystems.
func (c *Client) Close() error {
	var first error
	if c.sftp != nil {
		first = c.sftp.Close()
	}
	if err := c.client.Close(); err != nil && first == nil {
		first = err
	}
	return first
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
	candidates := defaultKeyFiles
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
		if err := os.MkdirAll(filepath.Dir(h.path), knownHostsDirPerms); err != nil {
			return err
		}
		f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, knownHostsFilePerms)
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
		return out.Bytes(), errBuf.Bytes(), fmt.Errorf("run %q: %w (stderr: %s)", cmd, err, truncate(errBuf.String(), stderrSnippetLen))
	}
	return out.Bytes(), nil, nil
}

// ErrNotExist is returned by ReadFile when the remote path does not exist.
var ErrNotExist = fs.ErrNotExist

// ReadFile fetches a remote file over SFTP (binary-safe, no shell
// involved). Missing files are reported as fs.ErrNotExist.
func (c *Client) ReadFile(path string) ([]byte, error) {
	sc, err := c.sftpClient()
	if err != nil {
		return nil, err
	}
	f, err := sc.Open(path)
	if err != nil {
		if isNotExist(err) {
			return nil, fmt.Errorf("%s: %w", path, fs.ErrNotExist)
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// Expand resolves a remote glob pattern to matching paths via the SFTP
// server-side glob. An empty result is not an error.
func (c *Client) Expand(pattern string) ([]string, error) {
	sc, err := c.sftpClient()
	if err != nil {
		return nil, err
	}
	return sc.Glob(pattern)
}

// isNotExist reports whether err is a remote "file not found", whether
// it arrives as a stdlib error or an SFTP status code.
func isNotExist(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var se *sftp.StatusError
	return errors.As(err, &se) && se.Code == uint32(sftp.ErrSSHFxNoSuchFile)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
