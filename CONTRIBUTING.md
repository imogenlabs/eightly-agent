# Contributing to Eight.ly Agent

Thanks for your interest in contributing. This document covers the basics.

## Getting Started

```bash
git clone https://github.com/imogenlabs/eightly-agent.git
cd eightly-agent
go build ./cmd/agent/
```

Requirements: Go 1.26.4 or later (`go.mod` pins toolchain 1.26.10, which Go fetches automatically), Docker (for integration testing), no CGO.

## Running Tests

```bash
make test
# or directly:
go test ./... -race
```

All tests must pass before submitting a PR.

## Code Style

- Run `go vet ./...` and `staticcheck ./...` before committing.
- No CGO -- the agent cross-compiles to six platforms with `CGO_ENABLED=0`.
- Keep packages small and focused. Handler logic stays thin; business logic goes in `internal/`.

## Documentation

The docs site is built from `docs/wiki` with MkDocs. Check a docs change the way CI does:

```bash
python3 -m venv /tmp/docsvenv && /tmp/docsvenv/bin/pip install -r requirements-docs.txt
/tmp/docsvenv/bin/mkdocs build --strict
```

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
