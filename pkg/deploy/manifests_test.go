package deploy_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

type kustomization struct {
	Resources []string `json:"resources"`
}

func TestMinikubeManifestsAreSelfContainedAndPinned(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	deployDir := filepath.Join(root, "deploy", "minikube")
	raw, err := os.ReadFile(filepath.Join(deployDir, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var k kustomization
	if err := yaml.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}
	if len(k.Resources) == 0 {
		t.Fatal("kustomization has no resources")
	}
	for _, resource := range k.Resources {
		resource := resource
		t.Run(resource, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(deployDir, resource))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(":latest")) {
				t.Fatalf("%s contains a mutable latest image", resource)
			}
			if bytes.Contains(raw, []byte("PRIVATE KEY")) || bytes.Contains(raw, []byte("stringData:\n  password:")) {
				t.Fatalf("%s contains credential material", resource)
			}
			decoder := utilyaml.NewYAMLToJSONDecoder(bufio.NewReader(bytes.NewReader(raw)))
			count := 0
			for {
				var object map[string]any
				if err := decoder.Decode(&object); err != nil {
					if err == io.EOF {
						break
					}
					t.Fatal(err)
				}
				if len(object) == 0 {
					continue
				}
				count++
				for _, field := range []string{"apiVersion", "kind", "metadata"} {
					if _, exists := object[field]; !exists {
						t.Fatalf("document %d is missing %s", count, field)
					}
				}
				if object["kind"] == "Secret" {
					t.Fatalf("static Secret found in %s; credentials must be generated at deploy time", resource)
				}
				if _, err := json.Marshal(object); err != nil {
					t.Fatalf("document %d is not JSON-compatible: %v", count, err)
				}
			}
			if count == 0 {
				t.Fatalf("%s has no Kubernetes objects", resource)
			}
		})
	}
}

func TestMinikubeScriptsAreSafeByDefault(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	scripts := []string{
		filepath.Join(root, "scripts", "minikube-deploy.sh"),
		filepath.Join(root, "scripts", "minikube-verify.sh"),
	}
	command := exec.Command("bash", append([]string{"-n"}, scripts...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bash syntax: %v: %s", err, output)
	}
	raw, err := os.ReadFile(scripts[0])
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{
		"PUBLIC_BIND_IP=${PUBLIC_BIND_IP:-127.0.0.1}",
		"IRONIC_CALLBACK_BASE_URL=${IRONIC_CALLBACK_BASE_URL:?",
		"IRONIC_CALLBACK_BIND_IP=${IRONIC_CALLBACK_BIND_IP:-${IRONIC_IMAGE_BIND_IP}}",
		"MINIKUBE_PROFILE:-go-metal3",
		"BMO_VERSION=v0.13.2",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("deploy script is missing pinned/safe setting %q", required)
		}
	}
	verifyRaw, err := os.ReadFile(scripts[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"get pvc ironic-data", "== Bound", "deployment/ironic deployment/go-metal3-api"} {
		if !bytes.Contains(verifyRaw, []byte(required)) {
			t.Fatalf("verify script is missing persistent/readiness check %q", required)
		}
	}
	for _, forbidden := range []string{"minikube delete", "kubectl delete namespace", "--insecure-skip-tls-verify"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("deploy script contains forbidden operation %q", forbidden)
		}
	}
	authConfig, err := os.ReadFile(filepath.Join(root, "deploy", "minikube", "ironic-api-config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"auth_strategy = http_basic", "http_basic_auth_user_file = /auth/ironic/htpasswd"} {
		if !bytes.Contains(authConfig, []byte(required)) {
			t.Fatalf("Ironic API authentication config is missing %q", required)
		}
	}
}

func TestAPIRBACIncludesEveryMutableSDKResource(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	for _, relative := range []string{"config/rbac/role.yaml", "deploy/minikube/api-rbac.yaml"} {
		raw, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, resource := range []string{
			"baremetalhosts", "bmceventsubscriptions", "dataimages", "hostclaims",
			"hostdeploypolicies", "hostfirmwarecomponents", "hostfirmwaresettings", "hostupdatepolicies",
		} {
			if !bytes.Contains(raw, []byte(resource)) {
				t.Fatalf("%s is missing %s permission", relative, resource)
			}
		}
	}
}

func TestIronicSQLiteUsesPersistentVolume(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "deploy/minikube/ironic.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"kind: PersistentVolumeClaim",
		"name: ironic-data",
		"claimName: ironic-data",
		"storage: 5Gi",
	} {
		if !bytes.Contains(raw, []byte(required)) {
			t.Fatalf("Ironic persistent SQLite manifest is missing %q", required)
		}
	}
}

func TestConsoleFeatureIsAbsent(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	for _, removed := range []string{
		filepath.Join(root, "cmd", "bmc-console-gateway"),
		filepath.Join(root, "pkg", "console"),
		filepath.Join(root, "deploy", "bmc-console"),
		filepath.Join(root, "deploy", "minikube", "bmc-console.yaml"),
	} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("console implementation still exists in SDK repository: %s", removed)
		}
	}
	for _, relative := range []string{
		"README.md",
		"docs/HTTP_API.md",
		"docs/MINIKUBE.md",
		"docs/STATUS.md",
		"deploy/minikube/api-rbac.yaml",
		"deploy/minikube/api.yaml",
		"deploy/minikube/ironic.yaml",
		"deploy/minikube/kustomization.yaml",
		"scripts/minikube-deploy.sh",
		"scripts/minikube-verify.sh",
	} {
		content, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"bmc-console", "BMC_BROKER", "services/proxy", "/vnc/vconsole", "/vnc/vmedia",
			"Console().", "actions/console", "console/capabilities", "console/sessions",
			"noVNC", "novnc", "ironic-console", "redfish-graphical",
		} {
			if bytes.Contains(bytes.ToLower(content), bytes.ToLower([]byte(forbidden))) {
				t.Fatalf("%s still contains removed console feature %q", relative, forbidden)
			}
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
