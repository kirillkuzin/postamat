# postamat

[![CI](https://github.com/kirillkuzin/postamat/actions/workflows/ci.yml/badge.svg)](https://github.com/kirillkuzin/postamat/actions/workflows/ci.yml)
![Coverage](.github/badges/coverage.svg)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

postamat is an open-source, agent-native secure transfer system. It coordinates transfer intent, agent presence, policy, audit, and WebRTC signaling while file bytes move directly between endpoints over encrypted WebRTC DataChannels.

The project is designed for self-hosting and for automation-first clients: local `postamat agentd` daemons, a human-facing CLI, browser recipient links, and future protocol adapters all use the same REST/WebSocket control plane.

> Status: active development. Core server, daemon, signaling, P2P chunking, and encryption foundations are in place, but polished end-user flows, deployment packaging, and stable APIs are still under implementation. Security reviews and design feedback are welcome.

## Project foundation

The repository currently contains the foundations for:

- agent-to-agent secure transfers through local `postamat agentd` daemons;
- CLI and MCP adapter commands for creating, listing, inspecting, and cancelling local agentd jobs;
- receiver-side agentd accept/deny policy for incoming transfer offers;
- browser `/p/{token}` recipient flow and signaling path;
- application-level end-to-end encryption for transferred chunks;
- backend-visible coordination metadata: transfer intent, lifecycle state, routing, policy, transient signaling, and audit events;
- canonical REST + WebSocket backend protocol;
- WebRTC DataChannel data plane with explicit chunk frames, backpressure handling, manifests, and SHA-256 integrity validation;
- self-hostable backend architecture with PostgreSQL-oriented persistence and room for TURN/TLS deployment assets;
- open-source project workflow: CI, contribution guide, security policy, issue templates, governance, and roadmap.

## Architecture

The short version:

```mermaid
flowchart LR
  agentA[Agent A] --> daemonA[postamat agentd A]
  agentB[Agent B] --> daemonB[postamat agentd B]
  human[Browser recipient] --> browser[/p/{token}]

  daemonA <-->|REST + WSS control| backend[postamat server]
  daemonB <-->|Outbound WSS control| backend
  browser <-->|WSS signaling| backend
  backend --> db[(PostgreSQL metadata + audit)]

  daemonA ==>|E2E encrypted WebRTC chunks| daemonB
  daemonA ==>|E2E encrypted WebRTC chunks| browser
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full project architecture: components, trust boundaries, control plane, data plane, transfer lifecycle, storage model, and deployment view.

## Repository layout

```text
cmd/
  postamat/       CLI entrypoint
  agentd/         local agent daemon entrypoint
  server/         backend server entrypoint
internal/
  agentd/         local daemon API, job state, backend client, inbox, Unix socket server
  agents/         agent identity, device registration, capabilities
  api/            REST routes, public recipient routes, WebSocket handlers
  app/            command wiring, CLI, and MCP adapter over local agentd
  audit/          audit event model and redaction
  auth/           bearer tokens, transfer tickets, scoped identities
  build/          build/CI invariants
  config/         default configuration
  db/             PostgreSQL-compatible repositories and schema tests
  p2p/            WebRTC transport, chunk protocol, E2E encryption, sender/receiver
  sessions/       transfer lifecycle domain model and repositories
  signaling/      presence registry, rooms, SDP/ICE envelope routing
migrations/       database migrations
web/recipient/    browser-recipient frontend source; dist is generated and ignored
deployments/      deployment manifests and reverse-proxy/TURN examples as they land
```

## Development

Requirements:

- Go 1.26.3 or newer compatible toolchain. Use the latest stable Go and latest stable dependencies at implementation time.
- Node.js 22 and npm for the browser-recipient frontend build.

Run full local verification:

```bash
make verify
```

The verify target installs and builds `web/recipient` before Go checks, matching CI.

Run Go tests only:

```bash
go test ./...
```

Build commands:

```bash
go build ./cmd/server
go build ./cmd/agentd
go build ./cmd/postamat
```

## CLI and MCP adapter

Start a local daemon socket:

```bash
postamat agentd
```

To create backend transfer records for local agent-to-agent sends and start the transfer-scoped signaling connection, configure the daemon identity and backend URL:

```bash
POSTAMAT_BACKEND_URL=https://postamat.example \
POSTAMAT_AGENT_ID=agent-a \
POSTAMAT_DEVICE_ID=laptop-1 \
postamat agentd
```

Use the CLI against that socket (`POSTAMAT_AGENTD_SOCKET` or `/tmp/postamat/agentd.sock` by default):

```bash
postamat send ./file.bin --to-agent agent-b
postamat share ./file.bin --browser-link
postamat status <transfer_or_job_id>
postamat cancel <transfer_or_job_id>
postamat list
postamat inbox
```

Automation clients can run `postamat mcp` and call the local MCP-style tools `create`, `status`, `cancel`, `list`, and `list_inbox`; the adapter stays a control-plane shim over `agentd` rather than moving file bytes through MCP.

## Deployment preview

The repository includes an early self-hosting stack under `deployments/`:

```bash
cp deployments/.env.example deployments/.env
# edit every change-me value before exposing the stack
docker compose --env-file deployments/.env -f deployments/compose.yaml up --build
```

The stack builds recipient frontend assets, packages the Go server/agentd/CLI binaries, and runs `postamat-server` behind Caddy with PostgreSQL and coturn services ready for the production persistence/WebRTC relay path. Runtime health endpoints are available at `/healthz` and `/metrics`.

## Contributing

postamat is an open-source project and welcomes issues, design discussion, documentation improvements, tests, and pull requests.

Start with:

- [CONTRIBUTING.md](CONTRIBUTING.md) for local workflow and PR expectations.
- [SECURITY.md](SECURITY.md) for vulnerability reporting and security invariants.
- [ROADMAP.md](ROADMAP.md) for current capabilities and open project gaps.
- [GOVERNANCE.md](GOVERNANCE.md) for maintainer and decision-making policy.
- [SUPPORT.md](SUPPORT.md) for where to ask questions.

## Development approach

Behavior changes should be test-first where practical:

1. Add or update a failing test for the intended behavior.
2. Verify the test fails for the expected reason.
3. Implement the smallest coherent change.
4. Run focused tests and then `make verify`.
5. Refactor with tests green.

## License

MIT. See [LICENSE](LICENSE).
