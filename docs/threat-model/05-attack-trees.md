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
│   └── 1.3 Exploit the --insecure-no-auth escape hatch
│       └── [RESIDUAL] operator-acknowledged; loud, deliberate opt-in only
├── 2. With a valid token, submit a host-escape create
│   ├── 2.1 HostConfig.Privileged=true / top-level Privileged on exec
│   │   └── [BLOCKED] r4 inspectCreateBody rejects both
│   ├── 2.2 Host PID/IPC/Network/Userns namespace
│   │   └── [BLOCKED] r4 isHostNamespace + UsernsMode=="host"
│   ├── 2.3 CapAdd: SYS_ADMIN / SYS_PTRACE / DAC_READ_SEARCH / ALL …
│   │   └── [BLOCKED] r4 dangerousCaps map (CAP_ prefix normalized)
│   ├── 2.4 Bind-mount /, /etc, /root, /proc, /sys, or docker.sock
│   │   └── [BLOCKED] r4 sensitivePaths via Binds + structured Mounts, cleaned + prefix-matched
│   └── 2.5 Escape via a vector NOT on the deny-list
│       ├── 2.5.1 Devices (e.g. /dev/mem, raw disk)        [RESIDUAL]
│       ├── 2.5.2 SecurityOpt (apparmor=unconfined, seccomp=unconfined) [RESIDUAL]
│       └── 2.5.3 Sysctls (kernel param tampering)         [RESIDUAL]
└── 3. Bypass the proxy filter entirely
    ├── 3.1 Smuggle the body past inspection (chunked/size games)
    │   └── [BLOCKED] r4 body buffered to maxInspectBody then replayed; non-create paths pass but carry no escape config
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
│   └── [BLOCKED] r3 lexical filepath.Rel reject + symlink-resolved containment
├── 2. Symlink redirection
│   ├── 2.1 Symlink at the final component on get/put
│   │   └── [BLOCKED] r3 O_NOFOLLOW open
│   ├── 2.2 Symlink mid-path resolves outside volume
│   │   └── [BLOCKED] r3 resolveExisting + Rel against resolved root
│   └── 2.3 MkdirAll follows a symlink during put
│       └── [BLOCKED] r3 re-validate containedPath AFTER MkdirAll
├── 3. Escape via rename newName
│   └── [BLOCKED] r3 validIdentifier + reject ../ + destination re-validated
└── 4. Exfiltrate a secret dotfile (.env)
    └── [BLOCKED] r3 dotfiles skipped on ls, refused on get
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

## Tree 5 — Steal the agent's long-lived credentials (A-03/04/05)

```
GOAL: Obtain the server token, pin, or TLS key
├── 1. Read a world/group-readable state or key file
│   └── [BLOCKED] r1 checkStatePerms (0600/0700 + uid) fail-closed; tightenKeyPerm repairs key mode
├── 2. Replay a stolen long-lived server token
│   └── [RESIDUAL] no agent-side token rotation (server-side lifecycle concern)
└── 3. Extract the secret from process memory / env
    └── [EXTERNAL] requires host root already
```
