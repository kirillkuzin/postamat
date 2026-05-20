# Architecture

The current architecture is intentionally small.

## MVP runtime mode

Only `webrtc_p2p` is active in the MVP.

```text
postamat senderd ==E2E encrypted chunks over WebRTC DataChannel==> browser
senderd <---WSS---> backend <---WSS---> browser
```

## Responsibility zones

- `sessions`: durable transfer session lifecycle.
- `signaling`: WebSocket rooms and SDP/ICE routing.
- `senderd`: local long-running sender runtime.
- `crypto`: application-level chunk encryption protocol.
- `api`: REST and WebSocket entrypoints.
- `db`: PostgreSQL persistence.
- `integrations`: MCP/ACP/CLI adapters.

## Backend contract

REST + WebSocket is canonical. MCP/ACP/CLI/SDK must use that contract or the local `senderd` control API; they should not duplicate business logic.
