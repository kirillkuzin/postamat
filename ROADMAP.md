# Roadmap

This roadmap describes public project gaps and directions. It is not a release promise; priorities may change based on security review, user feedback, and maintainer capacity.

## Current foundation

- Go backend with REST routes for transfer lifecycle and public recipient metadata.
- WebSocket signaling for agent presence, offers, acceptance, SDP, and ICE routing.
- Local `postamat agentd` APIs for send/receive job state and backend coordination.
- Browser recipient frontend served from `/p/{token}`.
- WebRTC DataChannel chunk transfer with backpressure-aware sender/receiver.
- Application-level E2E chunk encryption and manifest validation.
- PostgreSQL-compatible repositories and migrations for metadata/audit state.
- CI that builds frontend assets before Go tests, race tests, vet, coverage, and command builds.

## Open work

### Product and protocol completeness

- End-to-end browser receive implementation beyond signaling display.
- CLI UX for initiating, accepting, listing, and cancelling transfers.
- Durable transfer progress reporting across daemon restarts.
- Better failure taxonomy: transport failure, policy denial, expiry, cancellation, integrity failure.
- Large-file reliability: ACKs, resume, retry windows, and partial-file cleanup.

### Deployment and operations

- Docker Compose recipe for server, PostgreSQL, reverse proxy, and TURN/STUN.
- Example production configuration with TLS, secrets, CORS/origin policy, log redaction, and retention.
- Database migration runner and backup/restore notes.
- Metrics, structured logs, health checks, readiness checks, and operational runbooks.
- Release artifacts for Linux packages/containers and signed checksums.

### Security and privacy

- Threat model document with assets, adversaries, trust boundaries, and non-goals.
- External security review of transfer key delivery, WebRTC signaling, local daemon API, and inbox writes.
- Fuzzing for frame parsing, signaling envelopes, token/ticket verification, and HTTP handlers.
- Secrets handling guidelines for self-hosters.
- Browser-side review for URL fragment handling and no-key-leak regressions.

### Open-source community infrastructure

- Project Discussions or another public support channel.
- Maintainer triage labels and issue workflow.
- Release process and semantic versioning policy.
- Contributor recognition policy.
- Public project board once issue volume justifies it.

## Good first contributions

- Improve README examples and diagrams.
- Add CLI help text and examples.
- Add negative tests for malformed signaling envelopes and invalid chunk frames.
- Expand deployment documentation.
- Improve browser recipient accessibility and copy.
- Add small operational checks that keep `make verify` and CI aligned.
