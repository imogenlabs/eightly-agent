---
description: "Every Eight.ly Agent HTTP endpoint: health probes, metrics, host info, file browser, WebSocket console and the Docker API proxy."
---

# API Reference

All communication with Eight.ly Agent is over HTTPS. Every response includes two custom headers:

```
Eightly-Agent: <version>
Eightly-Agent-API-Version: 2
```

All endpoints except `/ping`, `/healthz`, `/readyz` and `/version` require a valid Bearer token. See [Authentication](Authentication.md) for token format. While an agent that registers with Eight.ly OS does not yet know its own ID, the authenticated endpoints answer [503](#503-service-unavailable).

---

## Health

### GET /healthz

Liveness check. No authentication required. Always returns 200 if the process is running.

**Response**: `200 OK`

```bash
curl -k https://localhost:9001/healthz
```

### GET /readyz

Readiness check. No authentication required. Returns 200 if the Docker daemon is reachable, 503 otherwise.

**Response**: `200 OK` or `503 Service Unavailable`

```bash
curl -k https://localhost:9001/readyz
```

### GET /version

Returns the agent version and API version. No authentication required.

**Response**: `200 OK`

```json
{
  "version": "1.0.0",
  "api_version": 2
}
```

```bash
curl -k https://localhost:9001/version
```

### GET /ping

Legacy health check endpoint. No authentication required.

**Response**: `200 OK` with an empty body.

```bash
curl -k https://localhost:9001/ping
```

---

## Metrics

### GET /metrics

Prometheus metrics endpoint. Requires authentication. Only available when the agent is started with `--metrics` or `EIGHTLY_METRICS_ENABLED=true`.

**Auth**: Required

**Response**: `200 OK` with Prometheus text exposition format.

```bash
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/metrics
```

Exposed metrics include:
- `eightly_agent_heartbeats_sent_total` -- total heartbeats sent
- `eightly_agent_heartbeats_failed_total` -- total heartbeat failures
- `eightly_agent_registration_attempts_total` -- total registration attempts
- Standard Go runtime metrics (goroutines, memory, GC)

---

## Host Info

### GET /host

Returns a summary of the host's PCI devices and physical disks: counts and total disk size only. Model and vendor strings are not sent, so the response does not fingerprint the hardware.

**Auth**: Required

**Response**: `200 OK`

```json
{
  "pci_count": 24,
  "disk_count": 2,
  "disk_total_gb": 1429
}
```

On Linux the data comes from sysfs (`/sys/bus/pci/devices` and `/sys/block`; loop, ram and device-mapper devices are skipped). On macOS it comes from `system_profiler` and `diskutil`, on Windows from PowerShell. Each of those reads is stopped after 15 seconds, and a part that times out is reported as empty. The summary is cached for 30 seconds, so frequent polling does not start new host commands each time.

```bash
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/host
```

---

## File Browser

All file browser endpoints operate on Docker volume data stored under `<volumes-path>/<volumeID>/_data/` (`--volumes-path`, by default `/var/lib/docker/volumes`). See [File Browser](File-Browser.md) for full details.

### GET /browse/ls

List the contents of a directory within a Docker volume.

**Auth**: Required

**Query Parameters**:

| Param | Required | Description |
|-------|----------|-------------|
| `volumeID` | Yes | Docker volume name or ID |
| `path` | No | Subdirectory path within the volume (default: root) |

**Response**: `200 OK`

```json
[
  {
    "name": "config.yml",
    "size": 1234,
    "dir": false,
    "modTime": "2026-05-20T10:30:00Z"
  },
  {
    "name": "data",
    "size": 4096,
    "dir": true,
    "modTime": "2026-05-19T08:15:00Z"
  }
]
```

```bash
curl -k -H "Authorization: Bearer $TOKEN" \
  "https://localhost:9001/browse/ls?volumeID=myvolume&path=/"
```

### GET /browse/get

Download a file from a Docker volume.

**Auth**: Required

**Query Parameters**:

| Param | Required | Description |
|-------|----------|-------------|
| `volumeID` | Yes | Docker volume name or ID |
| `path` | Yes | File path within the volume |

**Response**: The raw file content with appropriate Content-Type headers (served via Go's `http.ServeFile`).

```bash
curl -k -H "Authorization: Bearer $TOKEN" \
  "https://localhost:9001/browse/get?volumeID=myvolume&path=/config.yml" \
  -o config.yml
```

### POST /browse/put

Upload a file to a Docker volume. Uses multipart form data.

**Auth**: Required

**Query Parameters**:

| Param | Required | Description |
|-------|----------|-------------|
| `volumeID` | Yes | Docker volume name or ID |
| `path` | Yes | Destination path within the volume |

**Request Body**: Multipart form with a `file` field.

**Response**: `201 Created`

```json
{"status": "ok"}
```

```bash
curl -k -H "Authorization: Bearer $TOKEN" \
  -F "file=@local-config.yml" \
  "https://localhost:9001/browse/put?volumeID=myvolume&path=/config.yml"
```

The agent uses a 32 MB copy buffer. Parent directories are created automatically if they do not exist.

### DELETE /browse/delete

Delete a file or directory (recursively) from a Docker volume.

**Auth**: Required

**Query Parameters**:

| Param | Required | Description |
|-------|----------|-------------|
| `volumeID` | Yes | Docker volume name or ID |
| `path` | Yes | Path to delete |

**Response**: `204 No Content`

```bash
curl -k -X DELETE -H "Authorization: Bearer $TOKEN" \
  "https://localhost:9001/browse/delete?volumeID=myvolume&path=/old-data"
```

Warning: This uses `os.RemoveAll` and will recursively delete directories without confirmation.

### PUT /browse/rename

Rename a file or directory within a Docker volume.

**Auth**: Required

**Query Parameters**:

| Param | Required | Description |
|-------|----------|-------------|
| `volumeID` | Yes | Docker volume name or ID |

**Request Body**: JSON

```json
{
  "path": "/config.yml",
  "newName": "config-backup.yml"
}
```

**Response**: `200 OK`

```json
{"status": "ok"}
```

```bash
curl -k -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"path":"/config.yml","newName":"config-backup.yml"}' \
  "https://localhost:9001/browse/rename?volumeID=myvolume"
```

The `newName` must not contain `..` or `/` characters. The file is renamed within its current directory.

---

## WebSocket Console

Interactive container terminal access over WebSocket. See [WebSocket Console](WebSocket-Console.md) for protocol details.

### GET /websocket/attach

Attach to a running container's stdin/stdout/stderr streams.

**Auth**: Required

**Query Parameters**:

| Param | Required | Description |
|-------|----------|-------------|
| `id` | Yes | Container ID or name |

**Protocol**: WebSocket upgrade. After the upgrade, binary frames carry terminal I/O bidirectionally.

```
wss://localhost:9001/websocket/attach?id=my-container
```

The container is first checked as the proxy checks exec and attach (see the [WebSocket Console](WebSocket-Console.md#security-model)); one that reaches the host is refused with `403` before the upgrade. The agent then opens a raw TCP connection to the Docker socket and sends a `POST /containers/<full-id>/attach?stdin=1&stdout=1&stderr=1&stream=1` request with `Connection: Upgrade` and `Upgrade: tcp` headers. After Docker responds with `101 Switching Protocols`, the WebSocket and Docker streams are bridged.

### GET /websocket/exec

Start a previously created exec instance and stream its I/O.

**Auth**: Required

**Query Parameters**:

| Param | Required | Description |
|-------|----------|-------------|
| `id` | Yes | Exec instance ID (from `POST /containers/<id>/exec` via the Docker proxy) |

**Protocol**: WebSocket upgrade. Binary frames carry terminal I/O.

```
wss://localhost:9001/websocket/exec?id=<exec-id>
```

Typical usage flow:
1. Create an exec instance via the Docker proxy: `POST /containers/<id>/exec` with `{"Cmd":["/bin/bash"],"AttachStdin":true,"AttachStdout":true,"AttachStderr":true,"Tty":true}`
2. Connect to `/websocket/exec?id=<exec-id>` to start the session

The agent sends `POST /exec/<id>/start` with `{"Tty":true,"Detach":false}` and bridges the streams.

---

## Docker API Proxy

### Allowed Docker endpoints

Requests that do not match one of the agent's own endpoints go to the Docker proxy. Only the method and path pairs below are forwarded to the local Docker daemon socket; anything else is refused with **403**.

**Auth**: Required

The agent forwards to the Docker socket at the configured `--socket`. The following path rewrites are applied:

| Incoming Path | Proxied To |
|--------------|------------|
| `/v1.44/containers/json` | `/containers/json` (version prefix stripped) |
| `/containers` | `/containers/json` |
| `/images` | `/images/json` |
| `/volumes` | `/volumes` |
| `/networks` | `/networks` |

Version prefixes matching `/v<major>.<minor>` are stripped from all requests and the path is cleaned. The daemon receives the decoded path the filter checked, never the client's own escaping of it. `HEAD` is allowed wherever `GET` is.

The proxy sets `FlushInterval: 100ms` for streaming responses (logs, events, etc.).

| Method | Path | Checked |
|--------|------|---------|
| GET | `/_ping`, `/version`, `/info`, `/events`, `/system/df` | |
| GET | `/containers/json` | the command line of the agent's own container and of socket holders is shown as `<redacted>`, and secret flag values in every other |
| GET | `/containers/<id>/logs`, `/stats`, `/changes` | |
| GET | `/containers/<id>/json` | Env values redacted in the response (see below) |
| GET | `/containers/<id>/top` | refused for the agent's own container and any container holding the Docker socket |
| POST | `/containers/create` | body |
| POST | `/containers/<id>/start`, `/restart` | bind sources and local volumes bound under a root re-checked for symlinks when the agent runs on the host; restarting the agent's own container is refused |
| POST | `/containers/<id>/stop`, `/kill`, `/pause`, `/rename` | refused for the agent's own container; `/rename` also for a container holding the Docker socket |
| POST | `/containers/<id>/unpause`, `/wait`, `/resize` | |
| POST | `/containers/<id>/update` | body; refused for the agent's own container |
| POST | `/containers/<id>/exec` | body and the target container |
| POST | `/containers/<id>/attach`; GET `/containers/<id>/attach/ws` | the target container |
| PUT | `/containers/<id>/archive` | the target container |
| DELETE | `/containers/<id>` | refused for the agent's own container |
| POST | `/containers/prune` | |
| GET | `/exec/<id>/json` | |
| POST | `/exec/<id>/start` | the exec's container; a privileged exec is refused |
| POST | `/exec/<id>/resize` | |
| GET | `/images/json`, `/images/search`, `/images/<name>/json`, `/images/<name>/history` | |
| POST | `/images/create` | registry pulls only: `fromImage` required, `fromSrc` refused, no request body |
| POST | `/images/<name>/tag` | no request body; tagging into the agent's own image repository is refused |
| POST | `/images/prune`; DELETE `/images/<name>` | |
| GET | `/volumes`, `/volumes/<name>`; DELETE `/volumes/<name>`; POST `/volumes/prune` | |
| POST | `/volumes`, `/volumes/create` | body |
| GET | `/networks`, `/networks/<id>`; DELETE `/networks/<id>`; POST `/networks/prune`, `/networks/<id>/disconnect` | |
| POST | `/networks`, `/networks/create` | driver must be `bridge`, `macvlan` or `ipvlan` (or left out) |
| POST | `/networks/<id>/connect` | refused for a network whose driver is `host` |

Not on the list, among others: `/build`, `/session`, `/grpc`, `/commit`, `/containers/<id>/export`, downloading from `/containers/<id>/archive`, `/images/load`, `/images/get`, `/images/<name>/push`, `/auth`, `/distribution`, and everything under `/swarm`, `/services`, `/tasks`, `/nodes`, `/secrets`, `/configs` and `/plugins`, reads included.

#### Refused requests

The Docker socket is host root, so the proxy refuses requests that would let a container reach the host.

- **Container create, update and exec bodies** are refused with **403** when they ask for: `Privileged`; a PID, IPC, UTS, user or cgroup namespace other than a private one (`host` or `container:<id>`); `NetworkMode` `host`, `container:<id>`, or a network whose driver is `host` (also when named in `NetworkingConfig`); a capability outside Docker's default set (`AUDIT_WRITE`, `CHOWN`, `DAC_OVERRIDE`, `FOWNER`, `FSETID`, `KILL`, `MKNOD`, `NET_BIND_SERVICE`, `NET_RAW`, `SETFCAP`, `SETGID`, `SETPCAP`, `SETUID`, `SYS_CHROOT`), which rules out `NET_ADMIN`, `SYS_ADMIN`, `BPF`, `PERFMON` and the rest; `VolumesFrom`; a non-local `VolumeDriver`; any `SecurityOpt` other than `seccomp=builtin`, `apparmor=docker-default`, `no-new-privileges` and `label=user:`/`role:`/`level:`; `MaskedPaths` or `ReadonlyPaths`; OCI `Annotations`; a `Runtime` other than `runc` or `io.containerd.runc.v2`; `Devices`, `DeviceRequests` (GPUs, CDI) or `DeviceCgroupRules`; a sysctl outside `net.*`, `fs.mqueue.*` and the kernel IPC set; `CgroupParent`; `OomKillDisable`; a tmpfs over `/`, `/proc`, `/sys` or `/etc`; a mount type other than `bind`, `volume` or `tmpfs`; shared mount propagation. Fields are read both inside `HostConfig` and at the top level of the body, where exec create carries `Privileged`. A body that gives any key twice, at any depth and in any letter case, is refused: Docker would read the last one, which need not be the one checked.
- **Host paths.** A bind source (in `Binds`, a `bind` mount, or a local volume's `device`) must lie under one of the `--allowed-bind-roots`. The default is none: containers get named volumes only. A path with `..` is refused, and so is the Docker socket. `/etc/localtime` and `/etc/timezone` may be bound read-only. A `Binds` source is either an absolute path or a valid Docker volume name; a source with a `:`, a `//` prefix or a backslash, or a bind starting with a Windows drive (`c:/`), is refused, and so is a `volume` mount whose source is not a volume name. When the agent runs on the host itself, a bind, or a local volume bound to a path, that runs through a symlink below its root is refused at create and at start. When the agent runs in a container it cannot check that, so a restart policy other than `no` is refused on a container that mounts a path under a root (on create and on update): the daemon would mount it again on its own after a reboot or crash.
- **The agent's own data.** No container may mount a volume that the agent's own container mounts (its `/data`), bind a host path that its container binds, or, when the agent runs on the host, a path that holds or is its state, TLS key and certificate or secret file. The file browser refuses those volumes too.
- **Named volumes.** Every named volume a container create mounts is looked up, and one made on the host with bind options (`o=bind`, `device=/path`) is held to the same rule. `POST /volumes/create` applies it to `DriverOpts`; NFS, CIFS and tmpfs options are allowed, and options for any driver other than `local` are refused.
- **Existing containers.** Exec create, exec start, attach (HTTP and WebSocket, through the proxy or `/websocket/*`) and archive upload look up the target container and refuse the agent's own container and any container that is privileged, shares a host or another container's namespace, holds the Docker socket, binds a host path outside the roots, mounts a named volume that does, has a capability outside the default set, a host device, a security option or sysctl the create rules refuse, or a mount type other than bind, volume, tmpfs or image. A cgroup namespace of `host` is tolerated on an existing container, because cgroup v1 hosts give every container that by default; a request for it is refused. The masked-path, annotation and runtime rules apply to requests only. The request is forwarded with the container's full ID, so a rename between the check and the call cannot swap the target; the same is done for top, start, restart, stop, kill, pause, update, rename and delete. A container the daemon does not know is answered `404` by the agent and not forwarded. Starting, stopping and restarting such a container is not refused, except for the agent's own.
- **Container inspect** (`GET /containers/<id>/json`) answers with `EIGHTLY_*` environment values replaced by `<redacted>`, and every environment value replaced for the agent's own container and any container holding the Docker socket. Values of the agent's `--secret` and `--enroll-token` flags are redacted from `Args`, `Cmd` and `Entrypoint`.
- An inspected body that cannot be parsed is refused with **403**. `CapAdd`, `Binds`, `SecurityOpt`, `VolumesFrom` and `DeviceCgroupRules` may be a string or a list, as Docker accepts.
- An inspected body over 1 MiB is refused with **413**.

```json
{"message": "forbidden: Privileged=true rejected"}
```

```bash
# List containers
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/containers

# Inspect a container
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/containers/abc123/json

# Pull an image
curl -k -X POST -H "Authorization: Bearer $TOKEN" \
  "https://localhost:9001/images/create?fromImage=nginx&tag=latest"

# Any other call on the allow-list above is forwarded as sent
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/info
```

---

## Error Responses

### 401 Unauthorized

Returned for any authentication failure: missing token, bad signature, wrong issuer or type, missing `iat` or `exp`, a lifetime over 30 days, an expired token, or a token issued for a different agent.

```json
{"message": "unauthorized"}
```

### 403 Forbidden

Returned by the Docker API proxy for a refused request (see [Refused requests](#refused-requests)), and by `/websocket/attach` and `/websocket/exec`, before the upgrade, when the target container is refused by the same rules.

```json
{"message": "forbidden: <reason>"}
```

A Docker endpoint that is not on the [allow-list](#allowed-docker-endpoints) answers `forbidden: endpoint not allowed: <METHOD> <path>`. The file browser answers `403` with `{"error": ...}` for a hidden path (`forbidden`), a volume the agent may not read (`volume is not readable by the agent`) and, with `--browse-read-only`, a change (`read-only mode`).

### 503 Service Unavailable

Returned on every authenticated route while an agent that registers with Eight.ly OS does not yet know its own ID, with a `Retry-After: 30` header. It stops once registration succeeds.

```json
{"message": "service unavailable: agent is not registered with the server yet"}
```

`/readyz` also answers 503 when the Docker socket is unreachable, the file browser when its volumes folder does not exist, and the WebSocket console when 50 sessions are already open.

### 501 Not Implemented

Returned by the file browser when there is no volumes folder on this host: Docker Desktop keeps volumes inside its virtual machine. Set `--volumes-path` if they can be reached at some path.

### 413 Request Entity Too Large

Returned by the Docker API proxy when a body it must inspect is over 1 MiB.

```json
{"message": "too large: request body over the 1 MiB inspection limit"}
```

### 400 Bad Request

Returned for malformed requests (missing required parameters, bad JSON).

```json
{"error": "missing id param"}
```

### 500 Internal Server Error

Returned for server-side failures (filesystem errors, Docker socket errors).

```json
{"error": "detailed error message"}
```

## See Also

- [Authentication](Authentication.md) -- Token format and generation
- [File Browser](File-Browser.md) -- Detailed file browser documentation
- [WebSocket Console](WebSocket-Console.md) -- WebSocket protocol details
- [Troubleshooting](Troubleshooting.md) -- Debugging API issues
