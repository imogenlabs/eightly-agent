# Framework Crosswalk

Maps the agent's STRIDE work to external frameworks so the threat model can be
audited against industry baselines. Columns: STRIDE category → OWASP ASVS 4.0.3
chapter → OWASP Top 10 2021 → CWE → NIST SP 800-53 control family → MITRE ATT&CK
technique. Rows are keyed to the controls in `06-control-mapping.md`.

## STRIDE → framework matrix

| Agent control | STRIDE | OWASP ASVS | OWASP Top 10 | CWE | NIST 800-53 | MITRE ATT&CK |
|---------------|--------|-----------|--------------|-----|-------------|--------------|
| Fail-closed auth (no-secret → 503) | I/E | V1.4 Access Control Arch | A01 Broken Access Control | CWE-306 Missing Auth, CWE-636 Not-Failing-Securely | AC-3, AC-6 | T1190 Exploit Public-Facing App |
| HMAC-SHA256 Bearer verify | S | V2.1 / V3.5 Token | A07 Auth Failures | CWE-287, CWE-345 | IA-2, IA-5 | T1078 Valid Accounts |
| Constant-time compare (`hmac.Equal`) | S | V6.2.3 Crypto | A02 Crypto Failures | CWE-208 Timing Discrepancy | SC-13 | — |
| Docker endpoint allow-list + host-config checks (create/update/exec/volume/network) | E/T | V1.4 / V5.1 | A01 / A04 Insecure Design | CWE-269 Improper Privilege Mgmt, CWE-250 | AC-6, SC-39 | T1610 Deploy Container, T1611 Escape to Host |
| Bind roots (host paths only under `--allowed-bind-roots`, symlink check) | E | V12.3 File Exec | A01 | CWE-22 Path Traversal, CWE-59 Link Following, CWE-552 | AC-6, SC-39 | T1611 Escape to Host |
| Target-container checks (exec, attach, upload, WebSocket console) | E | V4.2 / V1.4 | A01 | CWE-269, CWE-668 Exposure to Wrong Sphere | AC-6 | T1609 Container Administration Command, T1611 |
| Inspect redaction | I | V8.3 / V14.3 | A01 | CWE-200 Info Exposure, CWE-532 | SC-28 | T1552 Unsecured Credentials |
| Inspection body cap (over 1 MiB → 413, never truncated) | D/T | V13.4 / V12.1 | A05 Misconfiguration | CWE-400 Uncontrolled Resource | SC-5 | T1499 Endpoint DoS |
| CSWSH origin enforcement | S/E | V13.5 WebSocket / V3.4 | A01 | CWE-346 Origin Validation, CWE-352 CSRF | AC-4, SC-7 | T1190 |
| Absent-Origin requires token | E | V13.5 | A01 | CWE-306 | AC-3 | T1190 |
| Docker ID validation (regex) | T | V5.1 Input Validation | A03 Injection | CWE-20 Improper Input, CWE-74 Injection | SI-10 | T1059 Command/Scripting |
| WS DoS bounds (cap/frames/timeouts) | D | V13.4 | A05 | CWE-400, CWE-770 Alloc w/o Limits | SC-5 | T1499 |
| Browse symlink containment | T/E | V12.3 / V5.1 | A01 | CWE-22 Path Traversal, CWE-59 Link Following | AC-3, AC-6 | T1083 File Discovery, T1611 |
| `O_NOFOLLOW` on get/put | E | V12.3 | A01 | CWE-59 Link Following, CWE-367 TOCTOU | SC-39 | T1611 |
| Hidden-path refusal | I | V14.3 / V8.1 | A01 | CWE-200 Info Exposure, CWE-538 | SC-28 | T1552 Unsecured Credentials |
| Mutate audit (actor/verb/path) | R | V7.1 Logging | A09 Logging Failures | CWE-778 Insufficient Logging | AU-2, AU-3 | T1070 Indicator Removal |
| Browse ReadOnly mode | T | V1.4 | A04 | CWE-269 | AC-6 | — |
| Host info minimization | I | V14.3 | A01 | CWE-200 Info Exposure | SC-28 | T1082 System Info Discovery |
| TOFU server-cert SPKI pin | S/T | V9.2 TLS / V6.4 | A02 / A08 Integrity Failures | CWE-295 Improper Cert Validation, CWE-297 | SC-8, SC-23 | T1557 Adversary-in-the-Middle |
| Pin persistence + restart seed | S | V9.2 | A02 | CWE-295 | SC-8 | T1557 |
| https-required server URL | T | V9.1 Comms | A02 | CWE-319 Cleartext Transmission | SC-8 | T1557 |
| State file perms/owner gate | I | V8.2 / V6.1 Data at Rest | A04 | CWE-276 Incorrect Default Perms, CWE-732 | AC-3, AC-6 | T1552 Unsecured Credentials |
| TLS key perm repair (0600) | I | V6.4 Key Mgmt | A02 | CWE-276, CWE-321 Hardcoded/Exposed Key | SC-12, SC-28 | T1552.004 Private Keys |
| TLS 1.2+ restricted ciphers | T | V9.1 / V9.3 | A02 | CWE-326 Inadequate Encryption | SC-8, SC-13 | T1557 |
| Security headers (HSTS/nosniff/DENY) | T | V14.4 HTTP Headers | A05 | CWE-693 Protection Mechanism Failure, CWE-1021 | SC-18 | T1189 Drive-by |
| Listener DoS bounds | D | V13.4 | A05 | CWE-400 | SC-5 | T1499 |
| Go 1.26.10 pinned + govulncheck 0 | T | V14.2 Dependency | A06 Vulnerable Components | CWE-1104 Unmaintained, CWE-937 | SA-11, RA-5 | T1195 Supply Chain |
| Digest-pinned bases + SHA-pinned Actions | T | V14.2 / V10.3 | A08 Integrity Failures | CWE-494 Download w/o Integrity Check, CWE-829 | SA-12, CM-7 | T1195.002 Compromise SW Supply |

## Coverage notes

- **OWASP Top 10 2021** — agent controls touch A01 (access control, the dominant
  theme), A02 (crypto/TLS/pinning), A03 (injection validation), A04 (insecure
  design / the endpoint allow-list as a design control), A05 (misconfiguration / headers / DoS),
  A06 (vulnerable components), A07 (auth), A08 (integrity / supply chain), and A09
  (logging). A10 (SSRF) is not directly applicable — the agent does not make
  attacker-controlled outbound requests; its only outbound is the pinned, fixed
  EOS server URL.
- **ASVS** — densest coverage in V1 (access control architecture), V9 (TLS), V12
  (file handling), and V13 (API/WebSocket). V7 logging is partial (events are
  emitted but lack an on-box integrity chain — see residual risk).
- **MITRE ATT&CK** — the agent's most-defended techniques are **T1611 Escape to
  Host** (endpoint allow-list, host-config and target checks, bind roots, symlink
  confinement) and **T1557 Adversary-in-the-Middle**
  (TOFU pin + TLS + https-required), reflecting that its highest-impact asset is
  host root behind the Docker socket.
- **NIST 800-53** — controls cluster in the **AC** (access control), **SC**
  (system/comms protection), **IA** (identity/auth), **AU** (audit), and **SA/CM**
  (supply chain / config management) families.
