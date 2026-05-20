# Agent Integrations

## agentd

`postamat agentd` is the local daemon near Hermes or another agent runtime. It owns long-running send/receive jobs, outbound WebSocket presence, incoming transfer offers, WebRTC sender/receiver state, file streaming, chunk encryption/decryption, inbox writes, and progress/cancel reporting.

The backend does not connect inbound to daemon ports. Each `agentd` keeps an outbound WSS connection to the backend; the server sends offers and control messages over that existing connection.

MVP daemon failure rule: if `agentd` exits, misses heartbeat, or cannot reconnect while a transfer is `connecting` or `transferring`, the transfer becomes `failed`. The agent creates a new transfer; same-transfer resume is deferred.

## MCP

MCP is a control plane to local `agentd`.

Planned tools:

```text
create_transfer(file_path, to_agent|browser_link, policy)
get_transfer_status(transfer_id)
cancel_transfer(transfer_id)
list_transfers(status?)
list_inbox()
```

A blocking `serve_file` tool may exist only for development demos, not for production workflow.

## ACP

ACP is deferred until a concrete target client/runtime is selected.

## Future resume

For large files later, add chunk manifest + per-chunk ACKs + resume from the last confirmed chunk + final content hash verification. Only then introduce `interrupted/retryable` as a non-terminal state.
