package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSecretFileTrimsFileTerminator(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "api-key")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET_FILE", secretPath)
	value, err := readSecretFile("TEST_SECRET_FILE")
	if err != nil {
		t.Fatal(err)
	}
	if value != "0123456789abcdef" {
		t.Fatalf("secret value = %q", value)
	}
}
