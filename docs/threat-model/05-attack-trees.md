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
│   ├── 2.1 Privileged=true (HostConfig, top level, or on exec create)
│   │   └── [BLOCKED] checkHostConfig on HostConfig and on the top-level fields
│   ├── 2.2 Host PID/IPC/network/user/UTS/cgroup namespace
│   │   └── [BLOCKED] isHostNamespace on PidMode, IpcMode, NetworkMode, UsernsMode, UTSMode, CgroupnsMode
│   ├── 2.3 CapAdd: SYS_ADMIN / SYS_PTRACE / DAC_READ_SEARCH / ALL …
│   │   └── [BLOCKED] dangerousCaps (CAP_ prefix normalized; list or single string)
│   ├── 2.4 Bind-mount a sensitive host path (Binds, Mounts Type "bind")
│   │   └── [BLOCKED] isSensitiveHostPath: path.Clean resolves .. and //, then refuses a path equal to,
│   │       under, or a parent of /, /etc, /root, /proc, /sys, /dev, /boot, /home, /run, /var/run,
│   │       /var/lib/docker, docker.sock (so /var and /var/lib are refused too)
│   ├── 2.5 Volume that mounts a host path or block device
│   │   ├── 2.5.1 POST /volumes/create DriverOpts type=none/o=bind/device=/path
│   │   │   └── [BLOCKED] checkVolumeOptions: bind/rbind, a local-path device, any type but nfs/nfs4/cifs/smb3/tmpfs,
│   │   │       and options for any non-local driver are refused
│   │   └── 2.5.2 Mounts Type "volume" with VolumeOptions.DriverConfig options
│   │       └── [BLOCKED] same checkVolumeOptions
│   ├── 2.6 Weaken the sandbox
│   │   ├── 2.6.1 SecurityOpt: seccomp unconfined or custom profile, apparmor other than docker-default,
│   │   │         label=disable or label=type:*, systempaths=unconfined, writable-cgroups=true
│   │   │   └── [BLOCKED] allowedSecurityOpt allow-list (seccomp=builtin, apparmor=docker-default,
│   │   │       no-new-privileges, label=user:/role:/level:)
│   │   ├── 2.6.2 MaskedPaths / ReadonlyPaths set (overrides the defaults; [] unmasks everything)
│   │   │   └── [BLOCKED] refused when present
│   │   ├── 2.6.3 Devices / DeviceCgroupRules (raw disk, /dev/mem)
│   │   │   └── [BLOCKED] refused on create and on POST /containers/{id}/update
│   │   ├── 2.6.4 Sysctls outside the namespaced set
│   │   │   └── [BLOCKED] only net.*, fs.mqueue.* and the kernel IPC set pass
│   │   ├── 2.6.5 CgroupParent
│   │   │   └── [BLOCKED] refused on create and update
│   │   └── 2.6.6 tmpfs over /, /proc, /sys, /etc (Tmpfs map or Mounts Type "tmpfs")
│   │       └── [BLOCKED] isSensitiveTmpfsTarget
│   ├── 2.7 Use the Swarm or plugin API
│   │   ├── 2.7.1 Install/enable a plugin (runs with host privileges)
│   │   │   └── [BLOCKED] any non-GET/HEAD under /plugins
│   │   ├── 2.7.2 Create a service/config/secret or change a node
│   │   │   └── [BLOCKED] any non-GET/HEAD under /services, /configs, /secrets, /nodes
│   │   └── 2.7.3 Read join tokens or the unlock key, init/join a swarm
│   │       └── [BLOCKED] every method under /swarm
│   └── 2.8 Escape via a vector NOT on the deny-list
│       ├── 2.8.1 A future HostConfig field that grants host reach      [RESIDUAL]
│       ├── 2.8.2 Join another container's namespaces (NetworkMode/PidMode "container:<id>")
│       │         or VolumesFrom a container created outside the proxy   [RESIDUAL]
│       ├── 2.8.3 A symlink on the host that points a harmless-looking bind path at a
│       │         sensitive one (paths are compared lexically)            [RESIDUAL]
│       └── 2.8.4 POST /build (e.g. networkmode=host) and NFS/CIFS volumes pointed at the host itself [RESIDUAL]
└── 3. Bypass the proxy filter entirely
    ├── 3.1 Smuggle the body past inspection
    │   ├── 3.1.1 Oversized body (padding past the inspection buffer)
    │   │   └── [BLOCKED] bodies over 1 MiB are refused with 413, never truncated and forwarded
    │   ├── 3.1.2 Body the filter cannot parse (wrong types, trailing data)
    │   │   └── [BLOCKED] any decode error is refused with 403; CapAdd/Binds/SecurityOpt/DeviceCgroupRules
    │   │       accept a string or a list, as Docker does
    │   └── 3.1.3 Path tricks (/v1.45/v1.45/containers/create, //, ./, ..)
    │       └── [BLOCKED] dockerPath cleans the path and strips every version prefix; the filter
    │           inspects exactly the path the proxy forwards
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
├── 2. Inject into the raw Docker hijack request
│   └── [BLOCKED] r2 validDockerID regex gates id before fmt into the raw request
└── 3. Exhaust the bridge to deny service to real operators
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
│   └── [BLOCKED] any path segment starting with . refused with 403 on every route
└── 5. Read a host disk through a device node a container created in its volume
    └── [BLOCKED] get/put serve regular files only, checked on the opened file
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
