package build_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRecipientFrontendProjectHasBuildScript(t *testing.T) {
	data, err := os.ReadFile("../../web/recipient/package.json")
	if err != nil {
		t.Fatalf("read web/recipient/package.json: %v", err)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatalf("decode package.json: %v", err)
	}
	if pkg.Scripts["build"] == "" {
		t.Fatal("package.json must define a build script")
	}
}

func TestGeneratedRecipientAssetsAreIgnored(t *testing.T) {
	data, err := os.ReadFile("../../.gitignore")
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	ignore := string(data)
	for _, want := range []string{"web/recipient/dist/", "web/recipient/node_modules/", "coverage.out", "coverage.txt"} {
		if !strings.Contains(ignore, want) {
			t.Fatalf(".gitignore must contain %q", want)
		}
	}
}

func TestMakeVerifyBuildsFrontendBeforeGoChecks(t *testing.T) {
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)
	assertInOrder(t, makefile,
		"cd web/recipient && npm ci --ignore-scripts",
		"cd web/recipient && npm run build",
		"gofmt",
		"go test ./...",
		"go test -covermode=atomic -coverprofile=coverage.out ./...",
		"go tool cover -func=coverage.out | tee coverage.txt",
		"go vet ./...",
		"go build ./cmd/server ./cmd/agentd ./cmd/postamat",
	)
}

func TestFocusedRaceChecksIncludeP2PPackage(t *testing.T) {
	makefileData, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	workflowData, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci workflow: %v", err)
	}
	for name, text := range map[string]string{"Makefile": string(makefileData), "ci.yml": string(workflowData)} {
		if !strings.Contains(text, "./internal/p2p") {
			t.Fatalf("%s focused race checks must include ./internal/p2p", name)
		}
	}
}

func TestGitHubActionsBuildsFrontendBeforeGoChecks(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci workflow: %v", err)
	}
	workflow := string(data)
	assertInOrder(t, workflow,
		"actions/setup-node",
		"npm ci --ignore-scripts",
		"npm run build",
		"gofmt",
		"go test ./...",
		"go test -covermode=atomic -coverprofile=coverage.out ./...",
		"go tool cover -func=coverage.out | tee coverage.txt",
		"go vet ./...",
		"go build ./cmd/server ./cmd/agentd ./cmd/postamat",
	)
}

func TestGitHubActionsPinsThirdPartyActionsToSHAs(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci workflow: %v", err)
	}
	workflow := string(data)
	for _, mutableRef := range []string{"actions/checkout@v4", "actions/setup-go@v5", "actions/setup-node@v4"} {
		if strings.Contains(workflow, mutableRef) {
			t.Fatalf("workflow uses mutable action ref %q", mutableRef)
		}
	}
	for _, action := range []string{"actions/checkout@", "actions/setup-go@", "actions/setup-node@"} {
		idx := strings.Index(workflow, action)
		if idx < 0 {
			t.Fatalf("workflow missing %q", action)
		}
		ref := workflow[idx+len(action):]
		ref = strings.Fields(ref)[0]
		if len(ref) != 40 || strings.Trim(ref, "0123456789abcdef") != "" {
			t.Fatalf("workflow action %q is not pinned to a 40-character SHA", action)
		}
	}
}

func assertInOrder(t *testing.T, text string, needles ...string) {
	t.Helper()
	position := -1
	for _, needle := range needles {
		next := strings.Index(text[position+1:], needle)
		if next < 0 {
			t.Fatalf("expected %q after byte %d", needle, position)
		}
		position += next + 1
	}
}
