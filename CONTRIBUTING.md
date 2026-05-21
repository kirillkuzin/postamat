# Contributing

Thanks for helping make postamat better. This project is open source and welcomes issues, design discussion, documentation, tests, and pull requests.

## Before you start

- Read [README.md](README.md) and [ARCHITECTURE.md](ARCHITECTURE.md).
- Check [ROADMAP.md](ROADMAP.md) and existing issues to avoid duplicate work.
- For security-sensitive findings, do **not** open a public issue. Follow [SECURITY.md](SECURITY.md).

## Development setup

Requirements:

- Go 1.26.3 or newer compatible toolchain.
- Node.js 22 and npm.

Run the full local verification pipeline:

```bash
make verify
```

This mirrors CI:

1. install browser-recipient dependencies;
2. build generated frontend assets;
3. check Go formatting;
4. run Go tests;
5. collect coverage;
6. run focused race tests;
7. run `go vet`;
8. build command binaries.

Useful focused commands:

```bash
go test ./...
go test -race ./internal/p2p
cd web/recipient && npm run build
```

## Pull request expectations

- Keep each PR focused on one coherent change.
- Add or update tests for behavior changes.
- Update README, ARCHITECTURE, SECURITY, or ROADMAP when user-facing behavior, security boundaries, or operational expectations change.
- Run `make verify` before requesting review.
- Use conventional commit-style PR titles when practical, for example `feat: add transfer resume state` or `docs: expand architecture trust boundaries`.

## Test-first development

For behavior changes, prefer this loop:

1. Add or update a failing test for the intended behavior.
2. Verify it fails for the expected reason.
3. Implement the smallest coherent change.
4. Run focused tests.
5. Run `make verify` before pushing.

## Security-sensitive code

Be especially careful around:

- transfer keys and key envelopes;
- URL fragments in browser recipient links;
- bearer tokens, public tokens, and transfer tickets;
- SDP/ICE payloads;
- local daemon Unix socket permissions;
- inbox path handling and metadata-derived filenames;
- logs and audit event payloads.

Do not log raw tokens, passwords, tickets, encryption keys, URL fragments, SDP/ICE payloads, or plaintext file contents.

## Issue triage

Useful issue categories:

- bug: something does not work as intended;
- security: public meta-issue only; private vulnerabilities go through SECURITY.md;
- docs: documentation improvements;
- good first issue: small, well-scoped contribution;
- help wanted: needs community implementation or design input;
- design: architecture/protocol discussion.
