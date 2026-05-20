# Security Notes

MVP security direction:

- TLS/WSS in production.
- Agent/device identities are explicit and scoped.
- Public/browser tokens have at least 128 bits of entropy and are stored hashed.
- API tokens are stored as hash + pepper.
- Sender/receiver tickets are short-lived and scoped to a transfer.
- Receiver policies/allowlists are checked before accepting agent-to-agent transfers.
- Backend does not store file bytes.
- Backend does not receive plaintext file bytes or raw transfer keys.
- Application-level E2E encryption is required before DataChannel send.
- Do not log raw tokens, passwords, tickets, keys, URL fragments, SDP/ICE payloads, or plaintext file contents.

Open design point: key delivery model for agent-to-agent and browser adapter.

## Reliability boundary

MVP integrity requires final manifest/checksum verification, but not resumable transfers. If the local daemon dies mid-transfer, the session fails; the system must not silently present a partial file as completed. Future resumable mode needs per-chunk ACKs, resume metadata, and final content hash verification.
