SHELL := /usr/bin/env bash

.PHONY: verify frontend-install frontend-build gofmt-check go-test go-race go-vet go-build
.NOTPARALLEL: verify

verify: go-build

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

go-race: go-test
	go test -race ./internal/agentd ./internal/auth ./internal/agents ./internal/sessions ./internal/api ./internal/audit ./internal/db ./internal/signaling

go-vet: go-race
	go vet ./...

go-build: go-vet
	go build ./cmd/server ./cmd/agentd ./cmd/postamat
