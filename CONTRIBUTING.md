# Contributing to Eight.ly Agent

Thanks for your interest in contributing. This document covers the basics.

## Getting Started

```bash
git clone https://github.com/imogenlabs/eightly-agent.git
cd eightly-agent
make build
```

Build from a clone with `make build`; `go install` is not supported for the v2.x tags because the module path has no `/v2` suffix.

Requirements: Go 1.26.4 or later (`go.mod` pins toolchain 1.26.10, which Go fetches automatically), Docker (for integration testing), no CGO.

## Running Tests

```bash
make test
# or directly:
go test ./... -race
```

All tests must pass before submitting a PR.

### Coverage floors

CI holds every package under `cmd/` and `internal/`, and the tree as a whole, to a coverage floor in `scripts/coverage-check.sh`:

```bash
go test ./... -covermode=atomic -coverprofile=coverage.out
scripts/coverage-check.sh coverage.out   # 0 held, 1 below a floor, 2 could not measure
scripts/coverage-check.sh --self-test    # proves the gate still tells those apart
```

A new package needs a floor of its own, or the gate fails. Set it about two points under what CI measures (the gate prints the number), and raise floors when coverage climbs. Floors are measured on Linux; on macOS or Windows the numbers differ and the run is advisory.

### End-to-end test

`test/e2e` builds the agent, enrolls it with a fake Eight.ly OS server and drives its API against the real Docker daemon: containers through the proxy, the refusals, the volume file browser, tokens for other agents and token renewal. It needs a local Docker daemon and takes about a minute:

```bash
EIGHTLY_E2E=1 go test -tags e2e -count=1 -v -timeout 10m ./test/e2e/
```

Set `EIGHTLY_E2E_DOCKER_SOCKET` if the socket is not `/var/run/docker.sock` (or `DOCKER_HOST`). It pulls `alpine:3.20` and removes every container and volume it made. CI runs it in the `e2e` job.

## Code Style

- Run `go vet ./...` and `staticcheck ./...` before committing.
- No CGO -- the agent cross-compiles to six platforms with `CGO_ENABLED=0`.
- Keep packages small and focused. Handler logic stays thin; business logic goes in `internal/`.

## Documentation

The [GitHub wiki](https://github.com/imogenlabs/eightly-agent/wiki) is generated; edit `docs/wiki/` in this repository.

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
