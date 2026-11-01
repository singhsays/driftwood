package model

import (
	"bytes"
	"driftwood/internal/paths"
	"fmt"
	"os"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

// EncryptArtifacts encrypts each artifact in place to an SSH public key
// recipient (age SSH recipients: ssh-ed25519 or ssh-rsa). Artifacts
// become binary and gain an ".age" suffix.
func EncryptArtifacts(arts []Artifact, pubKeyFile string) error {
	raw, err := os.ReadFile(paths.ExpandHome(pubKeyFile))
	if err != nil {
		return fmt.Errorf("read public key %s: %w", pubKeyFile, err)
	}
	rec, err := sshRecipient(raw)
	if err != nil {
		return err
	}
	for i := range arts {
		ciphertext, err := seal(rec, arts[i].Data)
		if err != nil {
			return fmt.Errorf("encrypt %s: %w", arts[i].Name, err)
		}
		arts[i].Data = ciphertext
		arts[i].Name += ".age"
		arts[i].Binary = true
	}
	return nil
}

// sshRecipient parses an authorized_keys-format public key into an age
// recipient.
func sshRecipient(raw []byte) (age.Recipient, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey(raw)
	if err != nil {
		// Also accept a bare public key blob without the authorized_keys
		// framing.
		if pub2, err2 := ssh.ParsePublicKey(raw); err2 == nil {
			pub = pub2
		} else {
			return nil, fmt.Errorf("parse public key: %w", err)
		}
	}
	switch pub.Type() {
	case ssh.KeyAlgoED25519:
		return agessh.NewEd25519Recipient(pub)
	case ssh.KeyAlgoRSA:
		return agessh.NewRSARecipient(pub)
	default:
		return nil, fmt.Errorf("unsupported key type %q for age encryption (want ssh-ed25519 or ssh-rsa)", pub.Type())
	}
}

// seal encrypts data to a single recipient.
func seal(rec age.Recipient, data []byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	w, err := age.Encrypt(buf, rec)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
