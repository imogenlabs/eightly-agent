---
description: "List, download, upload, delete and rename files in Docker volumes through Eight.ly Agent, and how paths are confined."
---

# File Browser

The file browser provides HTTP endpoints for managing files inside Docker volumes on the agent host. It supports listing directories, downloading files, uploading files, deleting files and directories, and renaming entries.

## How It Works

The file browser operates on Docker volume data stored on disk, in the folder where Docker keeps named volumes:

```
<volumes-path>/<volumeID>/_data/<path>
```

Where:
- `<volumes-path>` is `--volumes-path` / `EIGHTLY_VOLUMES_PATH` (see below for the default)
- `<volumeID>` is the Docker volume name or ID
- `_data` is the standard Docker volume subdirectory
- `<path>` is the user-specified path within the volume

For example, with default settings on Linux, a request for `volumeID=myapp_data&path=/config/app.yml` resolves to `/var/lib/docker/volumes/myapp_data/_data/config/app.yml`.

When `--volumes-path` is not set, the agent uses, in order:

1. `/var/lib/docker/volumes` (`C:\ProgramData\docker\volumes` on Windows) when it exists. That is where Docker keeps volumes, and where `docker-compose.yml` binds them into the agent's container.
2. `<data-path>/volumes` when it exists. Older instructions bound the volumes there (`/var/lib/docker/volumes:/data/volumes`); such installs keep working.
3. On Linux, `/var/lib/docker/volumes` anyway. Requests are answered `503` with a message naming that folder until it is bound.
4. On macOS and Windows, none. Docker Desktop keeps volumes inside its virtual machine, out of the agent's reach, and requests are answered `501`.

The agent's data directory (`--data`) holds only its own state, certificate and key; volumes are never read from it unless it holds a `volumes` folder as in item 2.

## Operations

### List Directory (ls)

```
GET /browse/ls?volumeID=<volume>&path=<dir>
```

Returns a JSON array of directory entries:

```json
[
  {
    "name": "app.yml",
    "size": 2048,
    "dir": false,
    "modTime": "2026-05-20T10:00:00Z"
  },
  {
    "name": "logs",
    "size": 4096,
    "dir": true,
    "modTime": "2026-05-19T22:00:00Z"
  }
]
```

Each entry includes:

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | File or directory name |
| `size` | int64 | Size in bytes |
| `dir` | bool | `true` if the entry is a directory |
| `modTime` | string | Last modification time (RFC 3339) |

Entries whose name starts with `.` are left out. Returns an empty array `[]` for empty directories. Returns `500` if the path does not exist, is not readable, or is not a directory (a file, a named pipe).

### Download File (get)

```
GET /browse/get?volumeID=<volume>&path=<filepath>
```

Serves the file using Go's `http.ServeContent`, which handles:
- Content-Type detection from the file extension
- Range requests for partial downloads
- If-Modified-Since / Last-Modified caching headers

The response body is the raw file content.

- Only regular files are served. A directory returns `400` "target is a directory"; a named pipe, socket or device node returns `400` "not a regular file".
- A symlink at the last path component returns `404`.

### Upload File (put)

```
POST /browse/put?volumeID=<volume>&path=<filepath>
```

Accepts a multipart form upload with a `file` field. The file is written to the resolved path on disk.

- Parent directories are created automatically (mode `0755`). New files get mode `0644`.
- An existing regular file is overwritten (truncated, then written).
- Returns `201 Created` with `{"status": "ok"}` on success.
- Returns `400 Bad Request` if the `file` form field is missing, the target is a directory, or the target is a named pipe, socket or device node. Nothing is written in those cases.

Uploads are capped at 100 MB per file; a larger body is rejected with `400`. The agent's `ReadTimeout` is 30 seconds and `WriteTimeout` is 30 minutes.

### Delete (delete)

```
DELETE /browse/delete?volumeID=<volume>&path=<filepath>
```

Deletes the file or directory at the specified path. For directories, this is a recursive delete (`os.RemoveAll`).

- Returns `204 No Content` on success.
- Returns `400 Bad Request` for the volume root itself.
- Deleting a symlink removes the link, never what it points to.
- There is no confirmation prompt or recycle bin. Deletion is immediate and permanent.

### Rename (rename)

```
PUT /browse/rename?volumeID=<volume>
Content-Type: application/json

{
  "path": "/config/old-name.yml",
  "newName": "new-name.yml"
}
```

Renames a file or directory within its current parent directory. The `newName` is a bare filename, not a path.

- Returns `200 OK` with `{"status": "ok"}` on success.
- Returns `400 Bad Request` if `newName` is not a plain name: it must start with a letter or digit and contain only letters, digits, `_`, `.` and `-` (so no `/`, no `..`, and no leading `.`). Also `400` for the volume root.
- Returns `403 Forbidden` if `path` is hidden (see [Hidden Files](#hidden-files)), so `.env` cannot be renamed to `env` and then downloaded.
- This cannot move files between directories -- only rename within the same directory.

## Path Format

Paths use forward slashes on every host OS (Linux, macOS and Windows). Both absolute and relative paths within the volume work:

- `/config/app.yml` -- absolute from volume root
- `config/app.yml` -- treated the same way after path joining

The `volumeID` parameter is the Docker volume name as it appears in `docker volume ls`.

## Security

### Path Traversal Protection

Every path is resolved inside the volume's directory and must stay there. A leading `/` means the volume root. Any `..` segment, a NUL byte, and Windows drive letters and network paths are rejected with `400 Bad Request`. Every operation goes through an `os.Root` opened on the volume's `_data` directory, which refuses a symlink or `..` that leads out of the volume at the moment of the operation; such a request fails (`404` for `get`, `500` for the others) and nothing outside the volume is touched. `get` and `put` also refuse a symlink at the last component: on Linux and macOS in the same system call (`O_NOFOLLOW`), on Windows by checking after the open that the path still names the file that was opened.

### Hidden Files

Any path with a segment that starts with `.` is refused with `403 Forbidden` on every route: `ls`, `get`, `put`, `delete` and the source path of `rename`. A `newName` starting with `.` is refused with `400` by the rename name check. This applies in read-write and read-only mode alike. `.ssh`, `.ssh/id_rsa`, `app/.git/config` and `.env` are never listed, read, written, deleted or renamed. On Windows a `\` also separates segments, so `app\.git` is refused too.

### Regular Files Only

`get` and `put` only read or write regular files. Named pipes, sockets and device nodes are refused with `400`: reading a pipe would hang the request, and a device node a container created in its own volume would reach a host disk. On Linux and macOS the file is opened non-blocking and checked with `fstat` before anything is read, truncated or written, so a pipe or device swapped in after an earlier check is still refused. On Windows the open is verified the same way after it happens, before any truncation.

### Read-Only Mode

With `--browse-read-only` (or `EIGHTLY_BROWSE_READ_ONLY=true`), `put`, `delete` and `rename` are refused with `403` "read-only mode". `ls` and `get` work as usual.

### Audit Log

Every `put`, `delete` and `rename`, including refused ones, is logged as a `browse.mutate` event with the caller (the token's `sub` claim), the verb, the path, the result and the remote address.

### Rename Validation

The `newName` field must be a plain name (see [Rename](#rename-rename)), so a rename can neither leave the directory nor create a hidden file.

### Authentication

All file browser endpoints require a valid Bearer token (see [Authentication](Authentication.md)). Without authentication enabled, anyone with network access to the agent can read, write, and delete files on the host.

### Volume Scope

The file browser only accesses files under `<volumes-path>/<volumeID>/_data/`. It cannot access arbitrary paths on the host filesystem, but every Docker volume on the host is within its reach.

## Docker Volume Layout

Docker stores named volume data at `/var/lib/docker/volumes/<name>/_data/`. When the agent runs in a container, bind that folder in at the same path, as `docker-compose.yml` does:

```yaml
volumes:
  - /var/lib/docker/volumes:/var/lib/docker/volumes
```

Leave the bind out to turn the file browser off; it then answers `503`. If Docker keeps its data elsewhere (`data-root` in `daemon.json`), bind `<data-root>/volumes` and set `--volumes-path` to where it lands. When the agent runs directly on the host as a non-root user (the `.deb`/`.rpm` service), it cannot read `/var/lib/docker/volumes`, which is root-only; the browser then answers `403` for each volume.

## Error Responses

| Status | Meaning |
|--------|---------|
| 200 | Directory listed, file downloaded, or file renamed |
| 201 | File uploaded |
| 204 | File or directory deleted |
| 400 | Path traversal or invalid `volumeID`, missing `file` field or upload over 100 MB, invalid `newName`, bad JSON body, target is a directory, target is not a regular file, or delete/rename of the volume root |
| 403 | Hidden path (a segment starting with `.`), read-only mode, or the volume directory is not readable by the agent |
| 404 | Volume not found, or (`get`) file not found or a symlink |
| 500 | Filesystem error (permission denied, path not found, not a directory for `ls`, disk full) |
| 501 | No volumes folder on this host: Docker Desktop keeps volumes inside its VM. Set `--volumes-path` if they can be reached |
| 503 | The volumes folder does not exist: bind Docker's volumes folder into the agent's container, or set `--volumes-path` |

## See Also

- [API Reference](API-Reference.md) -- Full endpoint documentation with curl examples
- [Authentication](Authentication.md) -- Token requirements for file browser access
- [Configuration](Configuration.md) -- `--data` setting
- [Troubleshooting](Troubleshooting.md) -- File browser 403 and permission issues
