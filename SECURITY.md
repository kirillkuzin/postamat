# Security Policy

postamat is pre-release. Please do not use it for sensitive production transfers yet.

## Reporting a vulnerability

Open a private security advisory on GitHub, or contact the maintainer directly if advisory reporting is not available.

## Security principles

- Backend must not store file bytes in the MVP.
- Backend must not receive plaintext file bytes or raw transfer keys.
- Do not log raw tokens, passwords, tickets, encryption keys, URL fragments, SDP/ICE payloads, or plaintext file contents.
- Store public tokens and API tokens only as hashes.
