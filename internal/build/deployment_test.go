package build_test

import (
	"os"
	"strings"
	"testing"
)

func TestDockerfileBuildsFrontendAndGoBinaries(t *testing.T) {
	data, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(data)
	assertInOrder(t, dockerfile,
		"FROM node:22",
		"npm ci --ignore-scripts",
		"npm run build",
		"FROM golang:1.26.3",
		"COPY --from=recipient-assets",
		"go build -o /out/postamat-server ./cmd/server",
		"go build -o /out/postamat-agentd ./cmd/agentd",
		"go build -o /out/postamat ./cmd/postamat",
		"HEALTHCHECK",
		"/healthz",
	)
}

func TestComposeDefinesServerPostgresProxyAndTurn(t *testing.T) {
	data, err := os.ReadFile("../../deployments/compose.yaml")
	if err != nil {
		t.Fatalf("read deployments/compose.yaml: %v", err)
	}
	compose := string(data)
	for _, want := range []string{
		"postamat-server:",
		"postgres:",
		"coturn:",
		"POSTAMAT_HTTP_ADDR=:8080",
		"POSTAMAT_DATABASE_URL=postgres://",
		"/healthz",
		"/metrics",
		"traefik-public:",
		"traefik.enable=true",
		"traefik.docker.network=traefik-public",
		"traefik.http.routers.postamat-http.entrypoints=web",
		"traefik.http.routers.postamat-https.entrypoints=websecure",
		"traefik.http.routers.postamat-https.tls.certresolver=letsencrypt",
		"Host(`${POSTAMAT_PUBLIC_HOST:?set POSTAMAT_PUBLIC_HOST}`)",
		"postamat-postgres:/var/lib/postgresql",
		"deployments/coturn/turnserver.conf",
		"--realm=${TURN_REALM:?set TURN_REALM}",
		"--static-auth-secret=${TURN_STATIC_AUTH_SECRET:?set TURN_STATIC_AUTH_SECRET}",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose.yaml missing %q", want)
		}
	}
	for _, forbidden := range []string{"POSTGRES_PASSWORD=postamat", "static-auth-secret=postamat", "caddy:"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compose.yaml must not embed default secret or proxy %q", forbidden)
		}
	}
}

func TestDeploymentExamplesDocumentRequiredSecrets(t *testing.T) {
	files := map[string][]string{
		"../../deployments/.env.example": {
			"POSTAMAT_PUBLIC_BASE_URL=https://postamat.crazybrain.space",
			"POSTAMAT_PUBLIC_HOST=postamat.crazybrain.space",
			"POSTAMAT_TOKEN_PEPPER=change-me",
			"POSTGRES_PASSWORD=change-me",
			"TURN_REALM=postamat.crazybrain.space",
			"TURN_STATIC_AUTH_SECRET=change-me",
		},
		"../../deployments/coturn/turnserver.conf": {
			"use-auth-secret",
			"no-cli",
		},
	}
	for path, wants := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}
