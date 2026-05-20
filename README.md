# postamat

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

postamat is an agent-first secure file sharing service. The MVP lets an online AI agent share a local file with a human recipient through a normal browser link.

The server coordinates sessions and WebRTC signaling. File bytes are streamed from the local sender daemon to the recipient browser over WebRTC DataChannel, with application-level end-to-end encryption planned from the first release.

> Status: early skeleton. The repository is not usable as a product yet.

## Goals

- Go backend for session lifecycle, auth, signaling, audit, and public recipient UI.
- Local `postamat senderd` sidecar near Hermes/agents for long-running P2P transfers.
- REST + WebSocket as the canonical backend protocol.
- MCP adapter as a control plane to `senderd`.
- Self-hosted deployment with PostgreSQL, coturn, and a TLS reverse proxy.

## MVP scope

Included:

- WebRTC P2P transfer from online sender daemon to browser.
- Public `/p/{token}` recipient flow.
- Agent REST API for create/status/list/cancel.
- Sender and receiver WebSocket signaling.
- TTL, password, max downloads, cancel/revoke.
- Metadata/audit storage only; no server-side file storage.

Not included in the MVP:

- Stored/offline download links.
- Reverse upload.
- Magic-Wormhole-style short-code live transfer.
- Enterprise IAM, billing, DLP, mobile apps.

## Architecture sketch

```text
Hermes / Agent
  -> postamat senderd
  -> backend REST + sender WSS

Recipient Browser
  -> /p/{token}
  -> public REST + receiver WSS

File path:
postamat senderd ==E2E encrypted chunks over WebRTC DataChannel==> Browser
```

## Repository layout

```text
cmd/
  postamat/      CLI entrypoint placeholder
  senderd/       sender daemon entrypoint placeholder
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
  signaling/     WSS rooms and SDP/ICE routing
  senderd/       local daemon runtime
  integrations/  MCP/ACP adapters
migrations/      database migrations
deployments/     Docker Compose and reverse proxy config
docs/            project documentation
```

Some directories are placeholders until their first tested implementation lands.

## Development

Requirements:

- Go 1.22+

Run tests:

```bash
go test ./...
```

Build skeleton binaries:

```bash
go build ./cmd/server
go build ./cmd/senderd
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
