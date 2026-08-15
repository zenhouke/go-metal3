package userdata

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LinuxUser is a provider-neutral cloud-init user description.
type LinuxUser struct {
	Name              string   `json:"name" yaml:"name"`
	SSHAuthorizedKeys []string `json:"ssh_authorized_keys,omitempty" yaml:"ssh_authorized_keys,omitempty"`
	Groups            []string `json:"groups,omitempty" yaml:"groups,omitempty"`
	Sudo              []string `json:"sudo,omitempty" yaml:"sudo,omitempty"`
	Shell             string   `json:"shell,omitempty" yaml:"shell,omitempty"`
	LockPassword      bool     `json:"lock_passwd" yaml:"lock_passwd"`
}

type CloudFile struct {
	Path        string `json:"path" yaml:"path"`
	Content     string `json:"content" yaml:"content"`
	Owner       string `json:"owner,omitempty" yaml:"owner,omitempty"`
	Permissions string `json:"permissions,omitempty" yaml:"permissions,omitempty"`
}

// CloudConfig describes security-conscious first-boot configuration.
type CloudConfig struct {
	Users           []LinuxUser `json:"users,omitempty" yaml:"users,omitempty"`
	DisableRoot     bool        `json:"disable_root" yaml:"disable_root"`
	SSHPasswordAuth bool        `json:"ssh_pwauth" yaml:"ssh_pwauth"`
	Packages        []string    `json:"packages,omitempty" yaml:"packages,omitempty"`
	WriteFiles      []CloudFile `json:"write_files,omitempty" yaml:"write_files,omitempty"`
	RunCommands     [][]string  `json:"runcmd,omitempty" yaml:"runcmd,omitempty"`
}

// RenderCloudConfig emits cloud-config YAML without interpolating values into
// hand-built documents.
func RenderCloudConfig(config CloudConfig) ([]byte, error) {
	if err := validateCloudConfig(config); err != nil {
		return nil, err
	}
	body, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal cloud-config: %w", err)
	}
	var out bytes.Buffer
	out.WriteString("#cloud-config\n")
	out.Write(body)
	out.WriteByte('\n')
	return out.Bytes(), nil
}

func validateCloudConfig(config CloudConfig) error {
	users := make(map[string]struct{}, len(config.Users))
	for _, user := range config.Users {
		if strings.TrimSpace(user.Name) == "" {
			return fmt.Errorf("cloud-config user name is required")
		}
		if _, exists := users[user.Name]; exists {
			return fmt.Errorf("duplicate cloud-config user %q", user.Name)
		}
		users[user.Name] = struct{}{}
		for _, key := range user.SSHAuthorizedKeys {
			trimmed := strings.TrimSpace(key)
			if !strings.HasPrefix(trimmed, "ssh-") && !strings.HasPrefix(trimmed, "ecdsa-") && !strings.HasPrefix(trimmed, "sk-") {
				return fmt.Errorf("user %q contains an invalid SSH public key", user.Name)
			}
		}
	}
	for _, file := range config.WriteFiles {
		if file.Path == "" || !filepath.IsAbs(file.Path) {
			return fmt.Errorf("cloud-config write_files path %q must be absolute", file.Path)
		}
	}
	for _, command := range config.RunCommands {
		if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
			return fmt.Errorf("cloud-config runcmd entries must not be empty")
		}
	}
	return nil
}
