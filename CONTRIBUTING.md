# Contributing

postamat is in early development.

## Local checks

```bash
gofmt -w ./cmd ./internal
go test ./...
```

## TDD expectation

For behavior changes, prefer test-first development:

1. Add or update a failing test.
2. Verify it fails for the expected reason.
3. Implement the minimal change.
4. Run the full suite.

## Security

Do not log raw tokens, passwords, tickets, encryption keys, URL fragments, or plaintext file contents.
