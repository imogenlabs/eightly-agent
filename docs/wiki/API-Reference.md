# API Reference

All communication with Eight.ly Agent is over HTTPS. Every response includes two custom headers:

```
Eightly-Agent: <version>
Eightly-Agent-API-Version: 2
```

All endpoints except `/ping` require a valid Bearer token. See [[Authentication]] for token format.

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

Returns information about the host machine's PCI devices and physical disks. This data is read from the Linux sysfs filesystem (`/sys/bus/pci/devices` and `/sys/block`).

**Auth**: Required

**Response**: `200 OK`

```json
{
  "pci_devices": [
    {
      "address": "0000:00:02.0",
      "vendor": "0x8086",
      "device": "0x5917",
      "class": "0x030000"
    }
  ],
  "physical_disks": [
    {
      "name": "sda",
      "size_gb": 476,
      "vendor": "Samsung "
    },
    {
      "name": "nvme0n1",
      "size_gb": 953,
      "vendor": ""
    }
  ]
}
```

PCI device fields are raw hex values from sysfs. Physical disks exclude loop, ram, and device-mapper (`dm-`) devices. Disk size is calculated from the sector count (`size * 512 / 1GB`).

```bash
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/host
```

---

## File Browser

All file browser endpoints operate on Docker volume data stored under `<data-path>/volumes/<volumeID>/_data/`. See [[File-Browser]] for full details.

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

Interactive container terminal access over WebSocket. See [[WebSocket-Console]] for protocol details.

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

The agent opens a raw TCP connection to the Docker socket and sends a `POST /containers/<id>/attach?stdin=1&stdout=1&stderr=1&stream=1` request with `Connection: Upgrade` and `Upgrade: tcp` headers. After Docker responds with `101 Switching Protocols`, the WebSocket and Docker streams are bridged.

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

### * /* (catch-all)

All requests that do not match a specific endpoint are proxied to the local Docker daemon socket.

**Auth**: Required

The agent acts as a reverse proxy, forwarding the request to the Docker Unix socket at the configured `--socket-path`. The following path rewrites are applied:

| Incoming Path | Proxied To |
|--------------|------------|
| `/v1.44/containers/json` | `/containers/json` (version prefix stripped) |
| `/containers` | `/containers/json` |
| `/images` | `/images/json` |
| `/volumes` | `/volumes` |
| `/networks` | `/networks` |

Version prefixes matching `/v<major>.<minor>` are stripped from all requests. Short paths for list endpoints are expanded to their full Docker API equivalents.

The proxy sets `FlushInterval: 100ms` for streaming responses (logs, events, etc.).

#### Refused requests

The Docker socket is host root, so the proxy refuses requests that would let a container reach the host. Paths are cleaned and every version prefix is stripped before the check, so the filter sees exactly the path the daemon will route.

- `POST /containers/create`, `POST /containers/<id>/exec` and `POST /containers/<id>/update` are refused with **403** when the body asks for: `Privileged`; host PID, IPC, network, user, UTS or cgroup namespace; a dangerous capability (`SYS_ADMIN`, `SYS_PTRACE`, `SYS_MODULE`, `SYS_RAWIO`, `DAC_READ_SEARCH`, `DAC_OVERRIDE`, `NET_ADMIN`, `SYS_BOOT`, `SYS_CHROOT`, `ALL`); a bind of `/`, `/etc`, `/root`, `/proc`, `/sys`, `/dev`, `/boot`, `/home`, `/run`, `/var/run`, `/var/lib/docker` or the Docker socket, a path under one of them, or a parent of one (such as `/var`); a volume mount whose driver options bind a host path or device; any `SecurityOpt` other than `seccomp=builtin`, `apparmor=docker-default`, `no-new-privileges` and `label=user:`/`role:`/`level:`; `MaskedPaths` or `ReadonlyPaths`; `Devices`; `DeviceCgroupRules`; a sysctl outside `net.*`, `fs.mqueue.*` and the kernel IPC set; `CgroupParent`; a tmpfs over `/`, `/proc`, `/sys` or `/etc`. Binds under ordinary data paths such as `/mnt/user/appdata` or `/opt/appdata`, and named volumes, are allowed.
- `POST /volumes/create` is refused when `DriverOpts` bind a host path or device. NFS, CIFS and tmpfs options are allowed; options for any driver other than `local` are refused.
- Every request under `/swarm`, and every request other than `GET`/`HEAD` under `/plugins`, `/services`, `/configs`, `/secrets` and `/nodes`, is refused with **403**.
- An inspected body that cannot be parsed is refused with **403**. `CapAdd`, `Binds`, `SecurityOpt` and `DeviceCgroupRules` may be a string or a list, as Docker accepts.
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

# Any other Docker Engine API call is forwarded as sent
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:9001/info
```

---

## Error Responses

### 401 Unauthorized

Returned for any authentication failure (missing token, bad signature, wrong issuer).

```json
{"message": "unauthorized"}
```

### 403 Forbidden

Returned by the Docker API proxy for a refused request (see [Refused requests](#refused-requests)).

```json
{"message": "forbidden: <reason>"}
```

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

- [[Authentication]] -- Token format and generation
- [[File-Browser]] -- Detailed file browser documentation
- [[WebSocket-Console]] -- WebSocket protocol details
- [[Troubleshooting]] -- Debugging API issues
