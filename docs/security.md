# Security Notes

MVP security direction:

- TLS/WSS in production.
- Public tokens have at least 128 bits of entropy and are stored hashed.
- API tokens are stored as hash + pepper.
- Passwords use Argon2id.
- Sender/receiver tickets are short-lived and scoped to a share.
- Backend does not store file bytes.
- Backend does not receive plaintext file bytes or raw transfer keys.
- Application-level E2E encryption is required before DataChannel send.
- Do not log raw tokens, passwords, tickets, keys, URL fragments, SDP/ICE payloads, or plaintext file contents.

Open design point: key delivery model.
