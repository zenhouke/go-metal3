package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigServiceAccountToken(t *testing.T) {
	cfg, err := LoadConfig(ConfigOptions{
		APIServer:           "https://kubernetes.example:6443/",
		ServiceAccountToken: "  token-value\n",
		CAFile:              "/secure/ca.crt",
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Host != "https://kubernetes.example:6443" || cfg.BearerToken != "token-value" {
		t.Fatalf("unexpected config: host=%q token=%q", cfg.Host, cfg.BearerToken)
	}
	if cfg.TLSClientConfig.CAFile != "/secure/ca.crt" {
		t.Fatalf("CAFile = %q", cfg.TLSClientConfig.CAFile)
	}
}

func TestLoadConfigServiceAccountTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(ConfigOptions{APIServer: "https://kubernetes.example", ServiceAccountTokenFile: path})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.BearerToken != "file-token" {
		t.Fatalf("BearerToken = %q", cfg.BearerToken)
	}
}

func TestLoadConfigServiceAccountRequiresServerAndToken(t *testing.T) {
	for name, opts := range map[string]ConfigOptions{
		"missing server": {ServiceAccountToken: "token"},
		"missing token":  {APIServer: "https://kubernetes.example"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(opts); err == nil {
				t.Fatal("LoadConfig() succeeded, want error")
			}
		})
	}
}
