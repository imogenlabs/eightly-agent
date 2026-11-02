# Data Flow Diagram

All flows into and out of the eightly-agent. Nodes in green are agent-owned code;
grey nodes are external trust domains. Edges are labelled with the protocol and the
control enforced at that crossing. Dashed edges are host-root reach.

```mermaid
flowchart TB
  EOS[EOS Server<br/>orchestrator]:::ext
  UI[Operator / UI / Browser<br/>TB-1 caller]:::ext
  CI[CI + GHCR<br/>A-13 build]:::ext

  subgraph AGENT["eightly-agent process (host root reach)"]
    HDR[headerMiddleware<br/>HSTS / nosniff / DENY]:::ours
    AUTH[auth.Middleware<br/>A-02 HMAC fail-closed]:::ours
    PROXY[proxy.NewDockerProxy<br/>TB-2 allow-list + checks]:::ours
    WS[websocket Attach/Exec<br/>TB-3 CSWSH + DoS]:::ours
    BROWSE[browse.Handler<br/>TB-4 confine + audit]:::ours
    HOST[hostinfo.Handler<br/>TB-5 minimized]:::ours
    REG[register heartbeat<br/>TB-6 TOFU pin]:::ours
    STATE[(state.json<br/>A-03/05/06 0600)]:::ours
    KEY[(cert.pem / key.pem<br/>A-04 0600)]:::ours
    LOG[[SECURITY / mutate logs<br/>A-12]]:::ours
  end

  DOCKER[(Docker socket<br/>A-01 host root)]:::ext
  VOL[(Volume FS<br/>A-08)]:::ext
  HW[(Host /proc + sysfs<br/>A-09)]:::ext

  UI -- HTTPS TLS1.2+ --> HDR
  HDR --> AUTH
  AUTH -- 401 / 503 fail-closed --> LOG
  AUTH -- authed --> PROXY
  AUTH -- authed --> WS
  AUTH -- authed --> BROWSE
  AUTH -- authed --> HOST

  PROXY -. allow-listed calls, checked bodies and targets .-> DOCKER
  WS -- target check --> PROXY
  WS -. unix hijack exec/attach .-> DOCKER
  BROWSE -. O_NOFOLLOW confined .-> VOL
  BROWSE -- mutate audit --> LOG
  HOST -- read minimized --> HW
  PROXY -- denied --> LOG
  WS -- denied --> LOG

  REG <-- HTTPS register/heartbeat<br/>TB-6 pin --> EOS
  REG -- read/write --> STATE
  AUTH -- verifies w/ A-02 --> STATE
  AGENT -- serves identity --> KEY
  CI -- pinned build --> AGENT

  classDef ext fill:#f6f8fa,stroke:#888,color:#111
  classDef ours fill:#dafbe1,stroke:#1f883d,color:#111
```

## Flow narrative

1. **Inbound API request** (`UI → headerMiddleware → auth.Middleware`). TLS
   terminates at the agent. Headers set, then the HMAC middleware verifies the
   Bearer token. No secret + no opt-out → 503 (fail-closed); an agent with a
   server URL that has not registered yet → 503 with `Retry-After: 30`;
   bad/absent token, or one issued for another agent → 401. Auth failures and
   the fail-closed case are `[SECURITY]`-logged.
2. **Authenticated request fans out** to one of four host-reaching handlers.
   Each applies its own boundary control *after* auth (defense in depth).
3. **Docker proxy** cleans the path, strips every version prefix, rewrites
   convenience paths, and refuses any method + path not on the allow-list. On
   container create, exec create, container update, volume create and network
   create it buffers the body (refused over 1 MiB), inspects it for host-escape
   config (host paths only under `--allowed-bind-roots`; named volumes looked up)
   and replays exactly those bytes to the socket. Exec, attach, exec start and
   archive upload look up the target container and go on under its full ID;
   container inspect responses have environment values redacted on the way back.
4. **WebSocket exec/attach** validates the Docker ID, checks the target container
   with the proxy's rules (refused with 403 before the upgrade), checks Origin (CSWSH),
   enforces the concurrency cap, dials Docker with bounded timeouts, hijacks, and
   bridges with frame-size limits, a write deadline, and idle/absolute timeouts.
5. **Browse** refuses `..` and hidden segments, runs every operation through an
   `os.Root` on the volume directory, opens files with `O_NOFOLLOW`, serves only
   regular files, and audits all mutations (and read-only rejections).
6. **Outbound to EOS** (register/heartbeat) enforces the TOFU SPKI pin on every
   dial and reads/writes the 0600 state file holding the token and pin.
