SHELL := /usr/bin/env bash

.PHONY: verify frontend-install frontend-build gofmt-check go-test go-coverage go-race go-vet go-build compose-config
.NOTPARALLEL: verify

verify: compose-config

frontend-install:
	cd web/recipient && npm ci --ignore-scripts

frontend-build: frontend-install
	cd web/recipient && npm run build

gofmt-check: frontend-build
	@if [ -n "$$(gofmt -l ./cmd ./internal)" ]; then \
		gofmt -l ./cmd ./internal; \
		echo "gofmt required"; \
		exit 1; \
	fi

go-test: gofmt-check
	go test ./...

go-coverage: go-test
	go test -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tee coverage.txt

go-race: go-coverage
	go test -race ./internal/agentd ./internal/auth ./internal/agents ./internal/sessions ./internal/api ./internal/audit ./internal/db ./internal/signaling ./internal/p2p

go-vet: go-race
	go vet ./...

go-build: go-vet
	go build ./cmd/server ./cmd/agentd ./cmd/postamat

compose-config: go-build
	docker compose --env-file deployments/.env.example -f deployments/compose.yaml config
