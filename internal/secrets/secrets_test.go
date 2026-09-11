// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealAndUnsealRoundTrip(t *testing.T) {
	t.Setenv("LIMELIT_SECRET", "a-test-secret")
	k, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	const secret = "sk-proj-abc123"
	sealed, err := k.Seal(secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, secret) {
		t.Fatal("the sealed value contains the plaintext")
	}
	got, err := k.Unseal(sealed)
	if err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	if got != secret {
		t.Errorf("round trip gave %q", got)
	}
}

func TestSealIsNotDeterministic(t *testing.T) {
	// A fresh nonce per value means two identical keys do not produce the
	// same ciphertext, so the database does not leak which rows match.
	t.Setenv("LIMELIT_SECRET", "a-test-secret")
	k, _ := Open(t.TempDir())
	first, _ := k.Seal("same")
	second, _ := k.Seal("same")
	if first == second {
		t.Error("sealing the same value twice produced identical ciphertext")
	}
}

func TestEnvironmentKeyWinsOverTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LIMELIT_SECRET", "")
	fileKeyring, err := Open(dir)
	if err != nil {
		t.Fatalf("Open with no env: %v", err)
	}
	sealed, _ := fileKeyring.Seal("value")

	t.Setenv("LIMELIT_SECRET", "a-different-secret")
	envKeyring, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envKeyring.Unseal(sealed); err == nil {
		t.Error("a value sealed with the file key opened with a different env key")
	}
}

func TestGeneratedKeyFileIsPrivateAndStable(t *testing.T) {
	// A self-hoster who never reads the docs still gets encryption at rest,
	// and the generated key has to survive a restart or every stored
	// credential becomes unreadable.
	dir := t.TempDir()
	t.Setenv("LIMELIT_SECRET", "")

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sealed, _ := first.Seal("value")

	info, err := os.Stat(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("the key file was not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %v, want 0600", perm)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := second.Unseal(sealed)
	if err != nil || got != "value" {
		t.Errorf("a restart could not read its own stored credential: %q, %v", got, err)
	}
}

func TestUnsealRejectsGarbage(t *testing.T) {
	t.Setenv("LIMELIT_SECRET", "a-test-secret")
	k, _ := Open(t.TempDir())
	for name, input := range map[string]string{
		"not base64": "!!!!",
		"too short":  "YWJj",
	} {
		if _, err := k.Unseal(input); err == nil {
			t.Errorf("Unseal accepted %s", name)
		}
	}
}

func TestUnsealAfterAKeyChangeSaysSo(t *testing.T) {
	// The error sends people to their key, not to their database.
	dir := t.TempDir()
	t.Setenv("LIMELIT_SECRET", "first")
	a, _ := Open(dir)
	sealed, _ := a.Seal("value")

	t.Setenv("LIMELIT_SECRET", "second")
	b, _ := Open(dir)
	_, err := b.Unseal(sealed)
	if err == nil {
		t.Fatal("a value opened under a different key")
	}
	if !strings.Contains(err.Error(), "the key changed") {
		t.Errorf("error = %q, want it to name the likely cause", err)
	}
}
