package model

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

// ageDecrypt decrypts an age file with an identity (test helper).
func ageDecrypt(data []byte, id *agessh.Ed25519Identity) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// keyFixture generates an ed25519 SSH keypair, writes the public half in
// authorized_keys format, and returns the identity for decryption.
func keyFixture(t *testing.T) (identity *agessh.Ed25519Identity, pubFile string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	pubFile = filepath.Join(t.TempDir(), "test.pub")
	authorized := ssh.MarshalAuthorizedKey(signer)
	if err := os.WriteFile(pubFile, authorized, 0o600); err != nil {
		t.Fatalf("write pub: %v", err)
	}
	id, err := agessh.NewEd25519Identity(priv)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	return id, pubFile
}

func TestEncryptArtifacts(t *testing.T) {
	id, pubFile := keyFixture(t)
	arts := []Artifact{
		{Name: "config.tar", Data: []byte("tar-bytes-\x00-\xff-sensitive"), Binary: true},
	}
	if err := EncryptArtifacts(arts, pubFile); err != nil {
		t.Fatalf("EncryptArtifacts: %v", err)
	}
	if arts[0].Name != "config.tar.age" {
		t.Errorf("name = %q, want config.tar.age", arts[0].Name)
	}
	if !arts[0].Binary {
		t.Error("encrypted artifact should be binary")
	}
	if bytes.Contains(arts[0].Data, []byte("sensitive")) {
		t.Error("plaintext leaked into ciphertext")
	}
	// header sanity: age armor-free binary starts with the age v1 magic.
	if !bytes.HasPrefix(arts[0].Data, []byte("age-encryption.org/v1")) {
		t.Errorf("not an age file: %q", arts[0].Data[:min(21, len(arts[0].Data))])
	}
	// Round-trip: the identity must decrypt back to the original.
	r, err := ageDecrypt(arts[0].Data, id)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(r) != "tar-bytes-\x00-\xff-sensitive" {
		t.Errorf("round-trip mismatch: %q", r)
	}
}

func TestEncryptArtifactsUnsupportedKey(t *testing.T) {
	_, pubFile := keyFixture(t)
	// Corrupt to an unknown key type by writing garbage.
	bad := filepath.Join(t.TempDir(), "bad.pub")
	if err := os.WriteFile(bad, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = pubFile
	if err := EncryptArtifacts([]Artifact{{Name: "x", Data: []byte("y")}}, bad); err == nil {
		t.Fatal("expected error for unparseable public key")
	}
}
