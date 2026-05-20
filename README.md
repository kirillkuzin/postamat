# postamat

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

postamat is an agent-native secure transfer control plane. The MVP lets online AI agents transfer files/data to other agents through Magic-Wormhole-like one-time WebRTC P2P sessions. A browser recipient link remains supported as an adapter flow.

The server coordinates transfer intent, agent presence, policy, audit, and WebRTC signaling. File bytes are streamed from local `postamat agentd` to another `agentd` or browser over WebRTC DataChannel, with application-level end-to-end encryption from the first release.

> Status: early skeleton. The repository is not usable as a product yet.

## Goals

- Go backend for transfer lifecycle, agent identity, policy, signaling, audit, and public recipient UI.
- Local `postamat agentd` near each agent for long-running bidirectional P2P transfers.
- Agent-to-agent transfer as the primary MVP flow.
- Browser `/p/{token}` recipient flow as an adapter.
- REST + WebSocket as the canonical backend protocol.
- MCP adapter as a control plane to local `agentd`.
- Self-hosted deployment with PostgreSQL, coturn, and a TLS reverse proxy.

## MVP scope

Included:

- WebRTC P2P transfer from online `agentd A` to online `agentd B`.
- Agent WSS presence and incoming transfer offers.
- Agent REST API for create/status/list/cancel.
- Receiver policy hooks and local inbox.
- Public `/p/{token}` recipient flow.
- SDP/ICE signaling over WSS.
- TTL, cancel/revoke, transfer failure/expiry handling.
- Metadata/audit storage only; no server-side plaintext file storage.
- Explicit lifecycle states and MVP failure behavior: if `agentd` dies during connection or transfer, the transfer fails and a new one must be created.

Not included in the MVP:

- Stored/offline encrypted blob fallback.
- Reverse upload.
- Human short-code CLI mode.
- Large-file resume/chunk ACKs.
- Enterprise IAM, billing, DLP, mobile apps.

## Architecture sketch

```text
Agent A -> postamat agentd A -> backend REST/WSS
Agent B -> postamat agentd B --outbound WSS--> backend

Signaling/control:
agentd A <---WSS---> backend <---WSS---> agentd B

File path:
agentd A ==E2E encrypted chunks over WebRTC DataChannel==> agentd B

Browser adapter:
agentd A ==E2E encrypted chunks over WebRTC DataChannel==> browser /p/{token}

MVP lifecycle:
created -> offered -> accepted -> connecting -> transferring -> completed
terminal: failed, cancelled, expired
```

## Repository layout

```text
cmd/
  postamat/      CLI entrypoint placeholder
  agentd/        agent daemon entrypoint placeholder
  server/        backend server entrypoint placeholder
internal/
  app/           application command wiring
  config/        defaults and config model
  sessions/      transfer session domain model
  api/           REST/WebSocket handlers
  auth/          tokens, tickets, scopes
  agents/        agent identity and capabilities
  crypto/        E2E chunk encryption protocol
  db/            repositories and migrations
  p2p/           WebRTC P2P orchestration
  policy/        transfer policy checks
  signaling/     WSS rooms, presence, SDP/ICE routing
  agentd/        local daemon runtime
  integrations/  MCP/ACP adapters
migrations/      database migrations
web/recipient/   browser-recipient frontend source; dist is generated and ignored
deployments/     Docker Compose and reverse proxy config
docs/            project documentation
```

Some directories are placeholders until their first tested implementation lands.

## Development

Requirements:

- Go 1.26.3 or newer compatible toolchain. Use latest stable Go and latest stable dependencies at implementation time.
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

Build skeleton binaries:

```bash
go build ./cmd/server
go build ./cmd/agentd
go build ./cmd/postamat
```

## Development approach

This project is developed test-first where possible:

1. Write a failing test for the next behavior.
2. Run it and verify the expected failure.
3. Implement the smallest code that passes.
4. Run the full test suite.
5. Refactor with tests green.

## License

MIT. See [LICENSE](LICENSE).
