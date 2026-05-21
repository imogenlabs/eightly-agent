# File Browser

The file browser provides HTTP endpoints for managing files inside Docker volumes on the agent host. It supports listing directories, downloading files, uploading files, deleting files and directories, and renaming entries.

## How It Works

The file browser operates on Docker volume data stored on disk. All paths are resolved relative to the agent's data directory:

```
<data-path>/volumes/<volumeID>/_data/<path>
```

Where:
- `<data-path>` is the agent's `--data-path` (default `/data`)
- `<volumeID>` is the Docker volume name or ID
- `_data` is the standard Docker volume subdirectory
- `<path>` is the user-specified path within the volume

For example, with default settings, a request for `volumeID=myapp_data&path=/config/app.yml` resolves to `/data/volumes/myapp_data/_data/config/app.yml`.

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

Returns an empty array `[]` for empty directories. Returns `500` if the path does not exist or is not readable.

### Download File (get)

```
GET /browse/get?volumeID=<volume>&path=<filepath>
```

Serves the file using Go's `http.ServeFile`, which handles:
- Content-Type detection from the file extension
- Range requests for partial downloads
- If-Modified-Since / Last-Modified caching headers

The response body is the raw file content.

### Upload File (put)

```
POST /browse/put?volumeID=<volume>&path=<filepath>
```

Accepts a multipart form upload with a `file` field. The file is written to the resolved path on disk.

- Parent directories are created automatically (`os.MkdirAll` with mode `0755`).
- Uses a 32 MB copy buffer for efficient large file transfers.
- Returns `201 Created` with `{"status": "ok"}` on success.
- Returns `400 Bad Request` if the `file` form field is missing.

There is no explicit file size limit enforced by the agent. The practical limit is determined by available disk space and the HTTP client's timeout settings. The agent's `ReadTimeout` is 120 seconds and `WriteTimeout` is 30 minutes, allowing large uploads.

### Delete (delete)

```
DELETE /browse/delete?volumeID=<volume>&path=<filepath>
```

Deletes the file or directory at the specified path. For directories, this is a recursive delete (`os.RemoveAll`).

- Returns `204 No Content` on success.
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
- Returns `400 Bad Request` if `newName` contains `..` or `/` characters.
- This cannot move files between directories -- only rename within the same directory.

## Path Format

Paths use forward slashes regardless of the host OS (the agent only runs on Linux). Both absolute and relative paths within the volume work:

- `/config/app.yml` -- absolute from volume root
- `config/app.yml` -- treated the same way after path joining

The `volumeID` parameter is the Docker volume name as it appears in `docker volume ls`.

## Security

### Path Traversal Protection

Both `volumeID` and `path` parameters are checked for `..` sequences. If either contains `..`, the request is rejected with `400 Bad Request`:

```json
{"error": "path traversal rejected"}
```

This prevents accessing files outside the volume data directory.

### Rename Validation

The `newName` field in rename requests is additionally checked for both `..` and `/` characters, preventing directory traversal through rename operations.

### Authentication

All file browser endpoints require a valid Bearer token (see [[Authentication]]). Without authentication enabled, anyone with network access to the agent can read, write, and delete files on the host.

### Volume Scope

The file browser only accesses files under `<data-path>/volumes/`. It cannot access arbitrary paths on the host filesystem. However, if the agent's data path is mounted to include Docker volume data (which it typically is), all Docker volume contents on that host are accessible.

## Docker Volume Layout

Docker stores named volume data at `/var/lib/docker/volumes/<name>/_data/`. For the file browser to work, the agent's data path must encompass this directory, or the volume data must be separately mounted into the agent's data path.

When running the agent in Docker, the typical setup is:

```yaml
volumes:
  - /var/lib/docker/volumes:/data/volumes:rw
```

This maps the host's Docker volume storage into the agent's expected path structure.

## Error Responses

| Status | Meaning |
|--------|---------|
| 400 | Path traversal detected, missing `file` field, invalid `newName`, or bad JSON body |
| 500 | Filesystem error (permission denied, path not found, disk full) |
| 201 | File uploaded successfully |
| 204 | File deleted successfully |
| 200 | Directory listed or file renamed successfully |

## See Also

- [[API-Reference]] -- Full endpoint documentation with curl examples
- [[Authentication]] -- Token requirements for file browser access
- [[Configuration]] -- `--data-path` setting
- [[Troubleshooting]] -- File browser 403 and permission issues
