package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestTokenKeyring(t *testing.T) {
	keyring.MockInit()

	if err := SetToken("https://jellyfin.example.com/", "alice", "tok-1"); err != nil {
		t.Fatalf("SetToken returned error: %v", err)
	}

	token, err := GetToken("https://jellyfin.example.com", "alice")
	if err != nil {
		t.Fatalf("GetToken returned error: %v", err)
	}
	if token != "tok-1" {
		t.Fatalf("expected token %q, got %q", "tok-1", token)
	}

	if err := DeleteToken("https://jellyfin.example.com", "alice"); err != nil {
		t.Fatalf("DeleteToken returned error: %v", err)
	}

	_, err = GetToken("https://jellyfin.example.com", "alice")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetTokenFallsBackToSecretTool(t *testing.T) {
	keyring.MockInitWithError(errors.New("dbus: connection closed by user"))

	binDir := t.TempDir()
	secretTool := filepath.Join(binDir, "secret-tool")
	if err := os.WriteFile(secretTool, []byte("#!/bin/sh\nprintf tok-from-secret-tool\n"), 0755); err != nil {
		t.Fatalf("write fake secret-tool: %v", err)
	}
	t.Setenv("PATH", binDir)

	token, err := GetToken("https://jellyfin.example.com", "alice")
	if err != nil {
		t.Fatalf("GetToken returned error: %v", err)
	}
	if token != "tok-from-secret-tool" {
		t.Fatalf("expected fallback token, got %q", token)
	}
}
