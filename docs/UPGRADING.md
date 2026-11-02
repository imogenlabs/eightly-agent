# Upgrading

## General upgrade process

> Download the new release from [GitHub Releases](https://github.com/imogenlabs/eightly-agent/releases),
> or use your package manager as shown below.

1. Download the new binary for your platform from [Releases](https://github.com/imogenlabs/eightly-agent/releases)
2. Stop the agent (`systemctl stop eightly-agent` / `eightly-agent.exe stop` / `docker compose down`)
3. Replace the binary
4. Start the agent
5. Verify with `eightly-agent version` or `GET /version`

For Docker: `docker compose pull && docker compose up -d` (image `ghcr.io/imogenlabs/eightly-agent`)
For Homebrew: `brew upgrade eightly-agent`

## Version history

### v1.0.0-beta1 → v1.0.0 (upcoming)

No breaking changes expected. The beta period is for collecting feedback on:
- Cross-platform compatibility (especially Windows service mode)
- Token expiry behavior
- TLS certificate SAN auto-detection

### Breaking change policy

- Minor versions (1.x) will not break the API, config flags, or env vars
- If a breaking change is unavoidable, it will be in a major version bump with migration docs
- Deprecations will be announced at least one minor version before removal
