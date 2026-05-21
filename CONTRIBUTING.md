# Contributing to Eight.ly Agent

Thanks for your interest in contributing. This document covers the basics.

## Getting Started

```bash
git clone https://github.com/smashingtags/eightly-agent.git
cd eightly-agent
go build ./cmd/agent/
```

Requirements: Go 1.23+, Docker (for integration testing), no CGO.

## Running Tests

```bash
make test
# or directly:
go test ./... -race
```

All tests must pass before submitting a PR.

## Code Style

- Run `go vet ./...` and `staticcheck ./...` before committing.
- No CGO -- the agent cross-compiles to five platforms with `CGO_ENABLED=0`.
- Keep packages small and focused. Handler logic stays thin; business logic goes in `internal/`.

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add volume snapshot support
fix: prevent path traversal in file browser
docs: update API reference for /host endpoint
test: add race-condition coverage for heartbeat loop
chore: bump gorilla/websocket to v1.5.4
```

## Pull Requests

- One logical change per PR.
- Large features or architectural changes need an issue discussion first -- open one before writing code.
- PRs must pass CI (lint, test, build matrix) before review.

## Security Issues

**Do not open public issues for security vulnerabilities.** Follow the disclosure process in [SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
