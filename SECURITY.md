# Security Policy

postamat is security-sensitive software. The project is under active development; review the code and deployment configuration carefully before using it for sensitive transfers.

## Reporting a vulnerability

Please do **not** report vulnerabilities in public issues.

Preferred process:

1. Open a private security advisory on GitHub for this repository.
2. Include affected commit/version, reproduction steps, impact, and any suggested fix.
3. If GitHub advisory reporting is unavailable, contact the maintainer through a private channel and avoid sharing exploit details publicly.

## Supported versions

Security fixes are currently made on the default branch until the project starts publishing stable releases. Once versioned releases exist, this file will list supported release lines.

## Security invariants

- The backend must not receive plaintext file bytes or raw transfer keys.
- File chunks are encrypted before they leave the sender runtime.
- Backend-visible payloads are limited to transfer intent, routing, lifecycle, policy, transient SDP/ICE signaling, and audit-safe metadata.
- SDP/ICE payloads can reveal network metadata; route them only as needed for connectivity, redact them from logs, and avoid persistent storage by default.
- Browser URL fragments may contain key material and must not be sent to the backend.
- Public tokens, API tokens, and transfer tickets must be stored as hashes or verifiable non-plaintext forms.
- Logs must redact raw tokens, passwords, tickets, encryption keys, URL fragments, SDP/ICE payloads, and plaintext file contents.
- Local daemon APIs should use restrictive Unix socket permissions and should not expose unsafe network listeners by default.
- Inbox writes must defend against path traversal, symlink races, collisions, and unsafe metadata-derived paths.

## Out of scope for public issues

If you discover any of the following, use private reporting:

- key leakage or plaintext disclosure;
- authentication or authorization bypass;
- remote code execution;
- path traversal or arbitrary file write;
- WebSocket identity spoofing or cross-transfer message routing;
- browser recipient URL-fragment leakage;
- dependency compromise affecting runtime security.
