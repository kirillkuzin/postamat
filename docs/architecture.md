# Architecture

The current architecture is intentionally small and agent-native.

## MVP runtime mode

Only `webrtc_p2p` is active in the MVP. Primary flow is agent-to-agent; browser recipient is an adapter.

```text
agentd A ==E2E encrypted chunks over WebRTC DataChannel==> agentd B
agentd A <---WSS---> backend <---WSS---> agentd B
```

Browser adapter:

```text
agentd A ==E2E encrypted chunks over WebRTC DataChannel==> browser
agentd A <---WSS---> backend <---WSS---> browser
```

## Responsibility zones

- `sessions`: durable transfer intent and lifecycle.
- `agents`: identity, devices, capabilities, presence.
- `policy`: allowlists, size limits, auto-accept/approval hooks.
- `signaling`: WebSocket rooms, presence, offers, SDP/ICE routing.
- `agentd`: local bidirectional daemon: file handles, inbox, WSS, WebRTC peer, encryption state, progress/cancel.
- `crypto`: application-level chunk encryption protocol.
- `api`: REST and WebSocket entrypoints.
- `db`: PostgreSQL persistence.
- `integrations`: MCP/ACP/CLI adapters.

## Backend contract

REST + WebSocket is canonical. MCP/ACP/CLI/SDK must use that contract or the local `agentd` control API; they should not duplicate business logic.

## Lifecycle and daemon failure

Canonical MVP states:

```text
created -> offered -> accepted -> connecting -> transferring -> completed
terminal: failed, cancelled, expired
```

If `agentd` dies or misses heartbeat/reconnect timeout during `connecting` or `transferring`, the backend marks the transfer `failed`. MVP does not implement `interrupted/retryable` or same-transfer resume; the agent creates a new transfer. Large-file resume is deferred until chunk ACKs and resume manifests exist.
