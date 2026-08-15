package userdata

import (
	"strings"
	"testing"
)

func TestRenderCloudConfig(t *testing.T) {
	t.Parallel()
	raw, err := RenderCloudConfig(CloudConfig{
		Users:       []LinuxUser{{Name: "metal3", SSHAuthorizedKeys: []string{"ssh-ed25519 AAAA test"}, LockPassword: true}},
		DisableRoot: true, WriteFiles: []CloudFile{{Path: "/etc/example", Content: "value"}}, RunCommands: [][]string{{"systemctl", "restart", "sshd"}},
	})
	if err != nil {
		t.Fatalf("RenderCloudConfig() error = %v", err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "#cloud-config\n") || !strings.Contains(text, "ssh_authorized_keys:") || !strings.Contains(text, "write_files:") {
		t.Fatalf("unexpected output:\n%s", text)
	}
}

func TestRenderIgnition(t *testing.T) {
	t.Parallel()
	if _, err := RenderIgnition([]byte(`{"ignition":{"version":"3.4.0"},"storage":{}}`)); err != nil {
		t.Fatalf("RenderIgnition() error = %v", err)
	}
	if _, err := RenderIgnition([]byte(`{"ignition":{"version":"2.3.0"}}`)); err == nil {
		t.Fatal("v2 Ignition document unexpectedly passed")
	}
}
