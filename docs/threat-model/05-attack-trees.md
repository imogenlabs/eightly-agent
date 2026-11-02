# Attack Trees

Attack trees for the highest-impact goals against the agent. Each leaf is marked
**[BLOCKED]** (a shipped control defeats it), **[RESIDUAL]** (accepted/deferred —
see `07-residual-risk.md`), or **[EXTERNAL]** (out of the agent's control). The
root goal in every tree is, ultimately, **host root**, because that is what the
Docker socket is.

## Tree 1 — Achieve host root via the Docker socket (A-01)

```
GOAL: Execute arbitrary code as root on a managed host
├── 1. Reach the Docker proxy without auth
│   ├── 1.1 Hit the agent before a secret is configured
│   │   └── [BLOCKED] r4 fail-closed: no secret + no --insecure-no-auth → 503
│   ├── 1.2 Forge a Bearer token
│   │   ├── 1.2.1 Guess/break the HMAC secret
│   │   │   └── [BLOCKED] r4 HMAC-SHA256, constant-time hmac.Equal; strength = A-02 entropy [RESIDUAL: secret strength is operator-supplied]
│   │   └── 1.2.2 Steal the secret from the host
│   │       └── [EXTERNAL] requires existing host access (already root)
│   ├── 1.3 Exploit the --insecure-no-auth escape hatch
│   │   └── [RESIDUAL] operator-acknowledged; loud, deliberate opt-in only
│   └── 1.4 Replay another agent's token (same fleet secret)
│       ├── 1.4.1 After the target has registered
│       │   └── [BLOCKED] sub must equal this agent's registered ID
│       └── 1.4.2 Before the target knows its ID
│           └── [BLOCKED] authenticated routes answer 503 until registration succeeds
├── 2. With a valid token, submit a host-escape request
│   ├── 2.0 Call an endpoint that carries host reach the filter does not read
│   │   │     (/build, /session, /grpc, /commit, export, archive download, image load/push,
│   │   │      Swarm, plugins, anything new)
│   │   └── [BLOCKED] allow-list of method + path (routes.go); everything else is 403
│   ├── 2.1 Privileged=true (HostConfig, top level, or on exec create)
│   │   └── [BLOCKED] checkHostConfig on HostConfig and on the top-level fields
│   ├── 2.2 Share a host namespace, or join another container's (container:<id>)
│   │   └── [BLOCKED] PID, IPC, UTS, user and cgroup modes must be private; NetworkMode may not be
│   │       host or container:<id>, nor a network whose driver is host (looked up by name or ID,
│   │       also for NetworkingConfig and /networks/{id}/connect); legacy Cgroup joins refused
│   ├── 2.3 CapAdd beyond the default set (SYS_ADMIN, BPF, PERFMON, SYS_RESOURCE, SYSLOG, NET_ADMIN, ALL …)
│   │   └── [BLOCKED] allowedCaps is Docker's default set; adding one back is a no-op, anything else is 403
│   ├── 2.4 Bind-mount a host path (Binds, Mounts Type "bind")
│   │   └── [BLOCKED] allowed only under --allowed-bind-roots (default: none, named volumes only);
│   │       ".." refused outright; a source with ":", a "//" prefix or a backslash is refused, and so
│   │       is any bind starting with a Windows drive ("c:/:c:/host" mounts C: on a Windows daemon);
│   │       a Binds or Mounts volume source must be a Docker volume name; /etc/localtime and
│   │       /etc/timezone read-only only; docker.sock refused; shared/rshared propagation refused
│   ├── 2.5 Volume that mounts a host path or block device
│   │   ├── 2.5.1 POST /volumes/create DriverOpts type=none/o=bind/device=/path
│   │   │   └── [BLOCKED] a bind device is held to the bind roots; a local-path device of any other
│   │   │       type, any type but nfs/nfs4/cifs/smb3/tmpfs, and options for non-local drivers are refused
│   │   ├── 2.5.2 Mounts Type "volume" with VolumeOptions.DriverConfig options
│   │   │   └── [BLOCKED] same checkVolumeOptions
│   │   ├── 2.5.3 Mount a named volume made on the host with o=bind,device=/
│   │   │   └── [BLOCKED] every named volume a create references is inspected (GET /volumes/{name})
│   │   │       and its options held to the same rule
│   │   └── 2.5.4 Mount the agent's own volume (its state, token and TLS key), or bind the host
│   │             directory holding them
│   │       └── [BLOCKED] the agent reads its own container's mounts once; those volumes and bind
│   │           sources, and on the host its state/key/cert/secret files, are off limits to every
│   │           bind, mount and local bind volume, whatever the roots say
│   ├── 2.6 Weaken the sandbox
│   │   ├── 2.6.1 SecurityOpt: seccomp unconfined or custom profile, apparmor other than docker-default,
│   │   │         label=disable or label=type:*, systempaths=unconfined, writable-cgroups=true
│   │   │   └── [BLOCKED] allowedSecurityOpt allow-list (seccomp=builtin, apparmor=docker-default,
│   │   │       no-new-privileges, label=user:/role:/level:)
│   │   ├── 2.6.2 MaskedPaths / ReadonlyPaths set (overrides the defaults; [] unmasks everything)
│   │   │   └── [BLOCKED] refused when present
│   │   ├── 2.6.3 Devices / DeviceCgroupRules / DeviceRequests (raw disk, GPU, CDI)
│   │   │   └── [BLOCKED] refused on create and on POST /containers/{id}/update
│   │   ├── 2.6.4 Sysctls outside the namespaced set
│   │   │   └── [BLOCKED] only net.*, fs.mqueue.* and the kernel IPC set pass
│   │   ├── 2.6.5 CgroupParent, OomKillDisable, a runtime other than runc, OCI annotations,
│   │   │         a non-local VolumeDriver
│   │   │   └── [BLOCKED] refused on create (and update, where the field exists)
│   │   └── 2.6.6 tmpfs over /, /proc, /sys, /etc (Tmpfs map or Mounts Type "tmpfs")
│   │       └── [BLOCKED] isSensitiveTmpfsTarget
│   ├── 2.7 Use the Swarm or plugin API
│   │   └── [BLOCKED] not on the allow-list, for any method, reads included
│   ├── 2.8 Copy another container's mounts with VolumesFrom (e.g. the agent's socket bind)
│   │   └── [BLOCKED] VolumesFrom refused outright; Eight.ly OS never sends it
│   ├── 2.8a Import an image from a URL or the body (POST /images/create fromSrc, in the query or a
│   │         form body the daemon reads first), or tag one into the agent's own repository
│   │   └── [BLOCKED] /images/create is a registry pull only: fromImage required, fromSrc refused,
│   │       no body accepted (so no form can override the query); /images/{name}/tag accepts no body
│   │       and refuses ghcr.io/imogenlabs/eightly-agent, smashingtags/eightly-agent and the
│   │       repository of the image the agent runs
│   ├── 2.9 Get inside a container that already reaches the host
│   │   ├── 2.9.1 Exec, attach or upload files (PUT archive) into the agent's own container or any
│   │   │         container that is privileged, shares a host or container namespace, holds the
│   │   │         socket, binds a path outside the roots, has an extra capability or a host device
│   │   │   └── [BLOCKED] the target is inspected for every such call; the forwarded path carries the
│   │   │       full ID that was checked, so a rename between check and use cannot swap containers.
│   │   │       A target the daemon does not know is answered 404 by the agent, never forwarded by
│   │   │       name (a protected container renamed to that name meanwhile would be reached unchecked);
│   │   │       the same holds for top, start, restart, stop, kill, pause, update, rename and delete,
│   │   │       all pinned to the inspected ID, and for /websocket/attach
│   │   ├── 2.9.2 POST /exec/{id}/start on an exec made elsewhere
│   │   │   └── [BLOCKED] the exec is inspected, its container checked as above, privileged execs refused
│   │   ├── 2.9.3 Read the agent's EIGHTLY_AGENT_SECRET from container inspect, list or top
│   │   │   └── [BLOCKED] inspect output: all Env values of a socket holder or the agent redacted,
│   │   │       EIGHTLY_* values redacted everywhere, secret flag values redacted; GET /containers/json
│   │   │       shows no command line for those containers and no secret flag value for any; top
│   │   │       refused there
│   │   ├── 2.9.4 /websocket/attach or /websocket/exec, which reach Docker without passing the proxy
│   │   │   └── [BLOCKED] proxy.TargetChecker applies the same target rules before the upgrade (403);
│   │   │       attach goes to the checked full ID, an unknown container is answered 404; privileged
│   │   │       and unknown exec instances refused
│   │   └── 2.9.5 Stop, kill, pause, remove, restart, update (memory=6m) or rename the agent's own
│   │             container; rename a socket holder
│   │       └── [BLOCKED] checkNotSelf on each of those routes
│   └── 2.10 Escape via a vector the allow-list still admits
│       ├── 2.10.1 A future HostConfig field that grants host reach      [RESIDUAL]
│       ├── 2.10.2 A container that can write under a bind root plants a symlink, and a second
│       │          bind (or a local volume bound under the root) follows it. Checked at create
│       │          and at start/restart, binds and bound volumes alike, on the host and, in a
│       │          container, under every root mounted into it at the same path (read-only).
│       │          A root a containerized agent cannot see gets no check, and restart policies
│       │          on containers under it are refused. Both modes: a link planted in the window,
│       │          or before a daemon-driven restart, still works  [RESIDUAL]
│       └── 2.10.3 NFS/CIFS volumes pointed at the host's own exports      [RESIDUAL]
└── 3. Bypass the proxy filter entirely
    ├── 3.1 Smuggle the body past inspection
    │   ├── 3.1.0 Say a key twice ({"Privileged":true,"Privileged":null}, or a case variant): the
    │   │         daemon's bool keeps true where a pointer would read nil
    │   │   └── [BLOCKED] any object, at any depth, with two keys Go's decoder folds alike is refused;
    │   │       Privileged is a bool, as it is in Docker
    │   ├── 3.1.1 Oversized body (padding past the inspection buffer)
    │   │   └── [BLOCKED] bodies over 1 MiB are refused with 413, never truncated and forwarded
    │   ├── 3.1.2 Body the filter cannot parse (wrong types, trailing data)
    │   │   └── [BLOCKED] any decode error is refused with 403; CapAdd/Binds/SecurityOpt/DeviceCgroupRules
    │   │       accept a string or a list, as Docker does
    │   └── 3.1.3 Path tricks (/v1.45/v1.45/containers/create, //, ./, ..)
    │       └── [BLOCKED] dockerPath cleans the path and strips every version prefix; the filter
    │           inspects exactly the path the proxy forwards, decoded (an escaped slash in the
    │           client's path never reaches the daemon)
    └── 3.2 Reach the unix socket directly
        └── [EXTERNAL] requires host-local access already
```

## Tree 2 — Drive a root container shell from a victim's browser (A-07)

```
GOAL: Run commands in a container as the operator, cross-origin
├── 1. CSWSH: trick an authenticated operator's browser into opening the WS
│   ├── 1.1 Cross-origin WS upgrade with the victim's cookies/creds
│   │   └── [BLOCKED] r2 checkOrigin runs in every branch; cross-origin rejected
│   ├── 1.2 Forge/omit the Origin header from a browser
│   │   └── [BLOCKED] r2 absent Origin allowed only WITH a valid Bearer token (browsers can't set it)
│   └── 1.3 Match a stale/over-broad allowlist entry
│       └── [RESIDUAL] correctness of operator-supplied --allowed-origins
├── 2. Use the console to reach a container that reaches the host (the agent's own, a socket holder)
│   └── [BLOCKED] the proxy's target check runs before the upgrade (Tree 1, 2.9.4)
├── 3. Inject into the raw Docker hijack request
│   └── [BLOCKED] r2 validDockerID regex gates id before fmt into the raw request
└── 4. Exhaust the bridge to deny service to real operators
    └── [BLOCKED] r2 50-session cap, 64KB frames, idle/absolute timeouts, bounded dial/handshake
```

## Tree 3 — Read/overwrite host files via browse (A-08)

```
GOAL: Read or write a host path outside the volume root
├── 1. Path traversal with ../
│   └── [BLOCKED] any .. segment refused; every operation runs in an os.Root on the volume
├── 2. Symlink redirection
│   ├── 2.1 Symlink at the final component on get/put
│   │   └── [BLOCKED] O_NOFOLLOW open (checked after the open on Windows)
│   ├── 2.2 Symlink mid-path resolves outside volume
│   │   └── [BLOCKED] os.Root refuses it at the moment of the operation
│   └── 2.3 MkdirAll follows a symlink during put
│       └── [BLOCKED] MkdirAll runs through the same os.Root
├── 3. Escape via rename newName
│   └── [BLOCKED] validIdentifier (no /, no .., no leading .) + rename inside the os.Root
├── 4. Exfiltrate a hidden file or directory (.env, .ssh)
│   ├── 4.1 Name it
│   │   └── [BLOCKED] any path segment starting with . refused with 403 on every route
│   └── 4.2 Reach it through a symlink alias (alias -> .ssh) the Root would follow
│       └── [BLOCKED] a directory on the way that is a symlink is refused, and ls refuses a final
│           one; a swap during the request stays inside the volume  [RESIDUAL, see 07]
├── 5. Read a host disk through a device node a container created in its volume
│   └── [BLOCKED] get/put serve regular files only, checked on the opened file
└── 6. Open the agent's own volume (state, token, key.pem), or a local volume bound to a
       host path outside the roots
    └── [BLOCKED] the browser asks the proxy's volume check before opening a volume: 403 for
        the agent's own volumes and out-of-root binds, 404 for an unknown volume
```

## Tree 4 — Impersonate the EOS server to the agent (A-06)

```
GOAL: Get the agent to trust a rogue orchestrator
├── 1. Present a different cert on a later dial
│   └── [BLOCKED] r1 TOFU pin: leaf SPKI mismatch → connection refused
├── 2. Win the very first connection (the TOFU window)
│   └── [RESIDUAL] classic TOFU first-contact risk; narrowed by optional --server-ca chain verify + preseed docs
├── 3. Force a pin re-capture by clearing persisted state
│   └── [BLOCKED] r1 state file is 0600/uid-gated; clearing it requires host access (already game over)
└── 4. Use --insecure-server-tls to drop verification
    └── [RESIDUAL] flag-only, loud, deliberate operator opt-in
```

## Tree 5 — Steal the agent's credentials (A-03/04/05)

```
GOAL: Obtain the server token, pin, or TLS key
├── 1. Read a world/group-readable state or key file
│   └── [BLOCKED] r1 checkStatePerms (0600/0700 + uid) fail-closed; tightenKeyPerm repairs key mode;
│       on Windows a protected access list (winacl) on the data folder, state, secret and key
├── 2. Replay a stolen server token
│   └── [RESIDUAL] valid until its exp (at most 30 days); the agent renews from half-life on
└── 3. Extract the secret from process memory / env
    └── [EXTERNAL] requires host root already
```
