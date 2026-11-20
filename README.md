<div align="center">

# Eight.ly Agent

**Manage Docker on any machine from your [Eight.ly OS](https://eight.ly) dashboard.**

Put the agent on a Linux, macOS or Windows host. Its containers, logs, consoles and volumes show up in Eight.ly OS next to the ones on the box itself.

[![Release](https://img.shields.io/github/v/release/imogenlabs/eightly-agent?style=flat-square&label=release&color=18181b)](https://github.com/imogenlabs/eightly-agent/releases/latest)
[![CI](https://img.shields.io/github/actions/workflow/status/imogenlabs/eightly-agent/ci.yml?branch=main&style=flat-square&label=CI)](https://github.com/imogenlabs/eightly-agent/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-18181b?style=flat-square)](LICENSE)

**[Docs](https://imogenlabs.github.io/eightly-agent/)** · **[Install](https://imogenlabs.github.io/eightly-agent/Installation/)** · **[Releases](https://github.com/imogenlabs/eightly-agent/releases)** · **[Security](SECURITY.md)** · **[Eight.ly OS](https://eight.ly)**

<br />

<!-- DEMO: replaced by the screen recording once it is uploaded -->
<img src="docs/images/agent-environments.webp" alt="Eight.ly OS listing two remote hosts connected through the agent" width="100%" />

</div>

## Connect a host in three steps

1. In Eight.ly OS, open **Environments → Add Agent**. It gives you one command that already holds the server address, the shared secret and a one-time enrollment token.
2. Run that command on the Docker host.
3. The host appears in Eight.ly OS as online. Pick it from the environment list and work with its containers.

The full walkthrough, with what each part of the command does, is in [Connect to Eight.ly OS](https://imogenlabs.github.io/eightly-agent/Installation/#connect-to-eightly-os).

## Install

The command from Eight.ly OS runs the container image. Every other way to install:

| | Platform | |
|:--|:--|:--|
| **Container** | Linux | `ghcr.io/imogenlabs/eightly-agent` · [Docker](https://imogenlabs.github.io/eightly-agent/Installation/#docker) · [Compose](https://imogenlabs.github.io/eightly-agent/Installation/#docker-compose-recommended) |
| **Homebrew** | macOS, Linux | `brew install imogenlabs/tap/eightly-agent` · [guide](https://imogenlabs.github.io/eightly-agent/Installation/#homebrew-macos-linux) |
| **Scoop** | Windows | `scoop bucket add eightly https://github.com/imogenlabs/scoop-eightly` then `scoop install eightly-agent` · [guide](https://imogenlabs.github.io/eightly-agent/Installation/#scoop-windows) |
| **Packages** | Debian, Ubuntu, RHEL, Fedora | `.deb` and `.rpm` on [Releases](https://github.com/imogenlabs/eightly-agent/releases/latest), with a hardened [systemd unit](https://imogenlabs.github.io/eightly-agent/Installation/#systemd-service) |
| **Windows service** | Windows | `.exe` on [Releases](https://github.com/imogenlabs/eightly-agent/releases/latest) · [guide](https://imogenlabs.github.io/eightly-agent/Installation/#windows-service) |
| **Binary** | Linux, macOS, Windows | Six builds on [Releases](https://github.com/imogenlabs/eightly-agent/releases/latest) · [guide](https://imogenlabs.github.io/eightly-agent/Installation/#binary-download) |

Images are built for amd64, arm64 and arm v7. Before updating, read [Upgrading](https://imogenlabs.github.io/eightly-agent/UPGRADING/). To remove the agent, see [Uninstall](https://imogenlabs.github.io/eightly-agent/Installation/#uninstall).

## What it does

| | |
|:--|:--|
| **Containers** | Start, stop, create and remove containers, images, networks and volumes through a checked [Docker API proxy](https://imogenlabs.github.io/eightly-agent/API-Reference/) |
| **Console** | Attach to a container or open a shell in it over a [WebSocket console](https://imogenlabs.github.io/eightly-agent/WebSocket-Console/) |
| **Files** | List, upload, download, rename and delete files in Docker volumes with the [file browser](https://imogenlabs.github.io/eightly-agent/File-Browser/) |
| **Host details** | Reports the host's disks and PCI devices to Eight.ly OS |
| **Stays connected** | [Registers](https://imogenlabs.github.io/eightly-agent/Registration-and-Heartbeat/) with a one-time token, then sends a heartbeat every 30 seconds |
| **Metrics** | Optional Prometheus endpoint, off by default ([configuration](https://imogenlabs.github.io/eightly-agent/Configuration/)) |

<table>
<tr>
<td width="50%"><img src="docs/images/agent-containers-local.webp" alt="Containers on the Eight.ly OS box" width="100%" /><p align="center"><sub>Containers on the box</sub></p></td>
<td width="50%"><img src="docs/images/agent-containers-remote.webp" alt="Containers on a remote host through the agent" width="100%" /><p align="center"><sub>The same screen for a remote host</sub></p></td>
</tr>
</table>

## What it will not do

The agent sits in front of the Docker socket, so it is built to refuse what would hand over the host:

- **Signed requests only.** Every call needs a token signed with the shared secret, naming this agent, and no more than 30 days old. [Authentication](https://imogenlabs.github.io/eightly-agent/Authentication/)
- **An allow-list, not a pass-through.** Only listed Docker endpoints are forwarded. Privileged containers, host namespaces and host paths outside the folders you allow are refused. [Allowed endpoints](https://imogenlabs.github.io/eightly-agent/API-Reference/#allowed-docker-endpoints)
- **Host folders are opt-in.** Containers get named volumes only, until you name the folders they may bind with `--allowed-bind-roots`. [Configuration](https://imogenlabs.github.io/eightly-agent/Configuration/)
- **Encrypted, and pinned.** The agent serves TLS and pins the Eight.ly OS server's key the first time it registers. [TLS](https://imogenlabs.github.io/eightly-agent/TLS/)
- **It protects itself.** Its own container, state and key cannot be stopped, mounted or read through it.

Keep port 9001 on a private network or VPN, never on the public internet. The full trust model, and what to do if the secret leaks, is in the [security model](https://imogenlabs.github.io/eightly-agent/Security-Model/). To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Reference

| | |
|:--|:--|
| [Configuration](https://imogenlabs.github.io/eightly-agent/Configuration/) | Every flag and environment variable, with defaults |
| [API reference](https://imogenlabs.github.io/eightly-agent/API-Reference/) | Endpoints, the Docker allow-list, error answers |
| [Architecture](https://imogenlabs.github.io/eightly-agent/Architecture/) | How a request travels from Eight.ly OS to the Docker socket |
| [Verifying releases](https://imogenlabs.github.io/eightly-agent/Verifying-Releases/) | Checksums, signatures, build provenance and the SBOM |
| [Troubleshooting](https://imogenlabs.github.io/eightly-agent/Troubleshooting/) | Symptoms, causes and fixes |
| [Changelog](CHANGELOG.md) | What changed in each release |

## Build from source

```bash
git clone https://github.com/imogenlabs/eightly-agent.git
cd eightly-agent
make build        # the binary lands in build/eightly-agent
```

Go 1.26 or newer. Targets, cross-compiling and the test suites are in [Build from source](https://imogenlabs.github.io/eightly-agent/Installation/#build-from-source) and [Development](https://imogenlabs.github.io/eightly-agent/Development/).

## License

[Apache License 2.0](LICENSE). The agent stays Apache 2.0; that will not change.

<div align="center"><sub>Built by <a href="https://imogenlabs.ai">Imogen Labs</a> for <a href="https://eight.ly">Eight.ly OS</a></sub></div>
