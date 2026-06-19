# Residual Risk Register

What the gauntlet did **not** eliminate, and why it is accepted or deferred. Each
row notes the compensating control that keeps the residual risk tolerable. These
are the **[RESIDUAL]** leaves from `05-attack-trees.md`.

| Risk | Why accepted/deferred | Owner decision | Compensating controls |
|------|----------------------|----------------|----------------------|
| **Docker socket is host root by design** | The agent's entire purpose is to broker the Docker API; the socket cannot be sandboxed away without removing the product. | ACCEPTED (design invariant) | r4 deny-list blocks the documented escape vectors; r4 fail-closed auth gates *who* reaches the proxy; mount the socket `:ro` (docs). |
| **Deny-list is non-exhaustive: `Devices`** | The r4 filter covers Privileged, host namespaces, dangerous caps, and sensitive binds — the well-documented escapes. `HostConfig.Devices` (e.g. mapping `/dev/mem` or a raw disk) is a real but lower-frequency escape not yet parsed. | DEFERRED | Auth (TB-1) still gates the caller; non-privileged device access is limited without a matching cap; candidate for a follow-up round. |
| **Deny-list is non-exhaustive: `SecurityOpt`** | `apparmor=unconfined` / `seccomp=unconfined` weaken the runtime sandbox but do not by themselves grant host FS/namespace access. Not yet inspected. | DEFERRED | Default Docker profiles remain unless explicitly disabled; caller is authenticated; flagged for the next deny-list pass. |
| **Deny-list is non-exhaustive: `Sysctls`** | Namespaced sysctls are container-scoped; the dangerous host-affecting ones generally require caps already on the deny-list. Not independently inspected. | DEFERRED | Dangerous-cap denial covers the common prerequisite; authenticated caller only. |
| **Browse `ReadOnly` present but default-off** | The `ReadOnly` option exists (`browse.ReadOnly(true)`), but the production wiring in `server.go` mounts browse mutable so the file-manager UX (upload/delete/rename) works. | ACCEPTED (operator/UX trade-off) | All mutations are confined (symlink/traversal) and audited (`browse.mutate`); deployments that don't need writes can opt into ReadOnly. |
| **No agent-side server-token rotation** | `State.Token` is long-lived; the agent has no protocol affordance to rotate it unilaterally. Rotation/expiry is an EOS-server responsibility. | DEFERRED (server-side) | r1 token stored 0600 + uid-gated; TOFU pin limits which server the token is ever sent to; documented in `register.go`. |
| **TOFU first-contact window** | The first dial to the EOS server captures the pin with no prior anchor — classic trust-on-first-use exposure if an attacker is already MITM at enrollment time. | ACCEPTED (TOFU model) | Optional `--server-ca` adds full chain verification on top of the pin; preseed/pinning docs; first contact is typically on a trusted provisioning network. |
| **Insecure escape hatches exist** (`--insecure-no-auth`, `--insecure-server-tls`) | Operators need a break-glass for local testing / bootstrap. | ACCEPTED | Both are deliberate, loud opt-ins; `--insecure-server-tls` is flag-only (never env-settable); `Validate()` rejects the silent-misconfig path (fail-closed default). |
| **HMAC secret strength is operator-supplied** | The agent verifies tokens with whatever secret it is given; a weak secret weakens A-02. | ACCEPTED (operator responsibility) | Constant-time `hmac.Equal`; fail-closed if no secret; provisioning should generate a high-entropy secret (CSPRNG). |
| **No on-box audit-log integrity chain** | `[SECURITY]` and `browse.mutate` lines are plain zerolog to stdout; a host-root attacker can alter them. | DEFERRED | Logs are greppable and shippable off-box by the operator's log pipeline; an attacker who can edit them already has host root (out of model). |
| **WS exec/attach has no per-session audit record** | The upgrade itself is `[SECURITY]`-gated at auth, but a started exec session isn't separately recorded. | DEFERRED | Auth + CSWSH gate session creation; Docker daemon logs the exec; candidate for parity with `browse.mutate`. |
| **`--allowed-origins` correctness is operator-supplied** | A stale or over-broad allowlist entry would admit an unintended origin. | ACCEPTED (operator config) | Default (empty) is strict same-origin via TLS SNI; absent-Origin still requires a token. |

## Review triggers

- New `HostConfig` field that affects host reach → re-evaluate the deny-list rows.
- A deployment enabling `browse.ReadOnly(true)` by default → update the ReadOnly row.
- Server-side token rotation shipping in EOS → close the token-rotation row.
- Any new inbound endpoint → re-run `04-stride-per-element.md` for that element.
