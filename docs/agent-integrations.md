# Agent Integrations

## senderd

`postamat senderd` is the local sidecar near Hermes or another agent runtime. It owns long-running share jobs, sender WebSocket connections, WebRTC sender state, file streaming, and progress/cancel reporting.

## MCP

MCP is a control plane to `senderd`.

Planned tools:

```text
create_p2p_share(file_path, ttl_seconds, max_downloads, password_mode)
get_p2p_share_status(share_id)
cancel_p2p_share(share_id)
list_p2p_shares(status?)
```

A blocking `serve_p2p_file` tool may exist only for development demos, not for production workflow.

## ACP

ACP is deferred until a concrete target client/runtime is selected.
