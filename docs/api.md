# API Draft

The API is not implemented yet. This draft captures the MVP shape.

## Agent API

```http
POST   /api/v1/p2p/shares
GET    /api/v1/p2p/shares/{share_id}
GET    /api/v1/p2p/shares?status=active
DELETE /api/v1/p2p/shares/{share_id}
GET    /api/v1/p2p/shares/{share_id}/sender/ws?ticket=...
```

## Public recipient API

```http
GET  /p/{token}
GET  /api/public/p2p/{token}
POST /api/public/p2p/{token}/verify-password
GET  /api/public/p2p/{token}/receiver/ws?ticket=...
```

## WebSocket messages

```text
sender.ready
receiver.joined
webrtc.offer
webrtc.answer
webrtc.ice
transfer.started
transfer.progress
transfer.completed
transfer.failed
session.cancelled
ping
pong
error
```
