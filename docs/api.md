# API Draft

The API is not implemented yet. This draft captures the agent-to-agent MVP shape.

## Agent REST API

```http
POST   /api/v1/transfers
GET    /api/v1/transfers/{transfer_id}
GET    /api/v1/transfers?status=active
DELETE /api/v1/transfers/{transfer_id}
GET    /api/v1/agents/{agent_id}
GET    /api/v1/agents?status=online
GET    /api/v1/agent/ws?ticket=...
```

Targets:

```json
{ "to": { "type": "agent", "agent_id": "agent-b" } }
{ "to": { "type": "browser_link" } }
```

## Public recipient API

```http
GET  /p/{token}
GET  /api/public/transfers/{token}
POST /api/public/transfers/{token}/verify-password
GET  /api/public/transfers/{token}/receiver/ws?ticket=...
```

## WebSocket messages

```text
agent.hello
agent.presence
transfer.offer
transfer.accepted
transfer.denied
webrtc.offer
webrtc.answer
webrtc.ice
transfer.started
transfer.progress
transfer.completed
transfer.failed
transfer.cancelled
transfer.expired
agent.disconnected
ping
pong
error
```

## Session lifecycle

MVP product states:

```text
created -> offered -> accepted -> connecting -> transferring -> completed
terminal: failed, cancelled, expired
```

Daemon failure policy: agent disconnect/heartbeat timeout during `connecting` or `transferring` marks the session `failed`. There is no same-transfer resume in MVP.
