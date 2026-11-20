---
description: "How Eight.ly Agent serves HTTPS with a generated certificate, how to supply your own, and how outbound TLS is verified."
---

# TLS

Eight.ly Agent always serves over HTTPS. There is no plaintext HTTP mode. On first start, the agent generates a self-signed TLS certificate if one is not already present. Custom certificates can be provided by placing them in the data directory.

## Auto-Generated Certificates

When the agent starts, it calls `tlsutil.EnsureCert(<data-path>)` which checks for:

- `<data-path>/cert.pem`
- `<data-path>/key.pem`

If **both** files exist, they are reused whatever their dates. The key must be readable by the agent only: on Linux and macOS a key with a mode wider than `0600` is tightened to `0600`, and the agent refuses to start if it cannot do that; on Windows the agent sets a protected access list on the key and certificate and refuses them if another account keeps access. If **either** file is missing, the agent generates a new self-signed certificate.

### Generated Certificate Properties

| Property | Value |
|----------|-------|
| Key type | RSA 2048-bit |
| Serial number | Random 128-bit integer |
| Subject CN | `eightly-agent` |
| Valid from | Time of generation |
| Valid until | 1 year from generation |
| Key usage | Digital Signature, Key Encipherment |
| Extended key usage | Server Authentication |
| SAN DNS names | `localhost` (plus any from `--tls-san`) |
| SAN IP addresses | `127.0.0.1` (plus any from `--tls-san`) |
| Key file permissions | `0600` (a protected access list on Windows) |

### File Locations

With the default `--data /data`:

```
/data/cert.pem    -- PEM-encoded X.509 certificate
/data/key.pem     -- PEM-encoded RSA private key (PKCS#1)
```

The data directory is created with mode `0700` if it does not exist. The key file is created with mode `0600` (owner read/write only) and the certificate with `0644`. On Windows the folder, key and certificate are closed to everyone but SYSTEM, Administrators and the agent's own account before the key is written.

### Extra SANs via `--tls-san`

To include additional DNS names or IP addresses in the auto-generated certificate, use the `--tls-san` flag (or `EIGHTLY_TLS_SANS` env var) with a comma-separated list:

```bash
./eightly-agent --tls-san "docker-host-01.local,192.0.2.50"
```

This is useful when the agent is accessed by hostname or LAN IP in addition to localhost, avoiding certificate name mismatch warnings without needing to generate custom certificates.

### Limitations of Auto-Generated Certs

- The certificate is self-signed, so clients must either trust it explicitly (via `--server-ca` on the EOS side) or use `-k` in curl for debugging only.
- Without `--tls-san`, the SAN only covers `localhost` and `127.0.0.1`. Connecting via hostname or LAN IP will trigger certificate name mismatch warnings.
- The certificate's validity ends 1 year after generation and it is not renewed. The agent keeps serving it after that date on purpose: Eight.ly OS pins this exact certificate (see below), so a new one would cut the agent off. The container health check (`eightly-agent healthcheck`) also accepts only this exact certificate and ignores its dates, so an agent past the certificate's first year stays healthy.

## Custom Certificates

To use your own TLS certificates (e.g., from Let's Encrypt, an internal CA, or a manually generated cert):

1. Place `cert.pem` and `key.pem` in the agent's data directory **before starting the agent**.
2. Make the key mode `0600` and owned by the user the agent runs as (uid 1000 in the container image, `eightly-agent` for the packages). The agent refuses a key it cannot tighten to `0600`.
3. The agent will find them and use them without generating new ones.

### Docker Mount Example

```yaml
services:
  eightly-agent:
    image: ghcr.io/imogenlabs/eightly-agent:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /etc/eightly-agent/cert.pem:/data/cert.pem:ro
      - /etc/eightly-agent/key.pem:/data/key.pem:ro
      - agent-data:/data
```

Note: Mount the cert and key files individually as read-only. The agent's data volume is also mounted at `/data` for state file storage; Docker mounts `/data` first and the two files over it. A read-only key cannot be tightened, so it must already be `0600` and owned by uid 1000 on the host.

### Binary Example

```bash
mkdir -p /var/lib/eightly-agent
cp /path/to/cert.pem /var/lib/eightly-agent/cert.pem
cp /path/to/key.pem /var/lib/eightly-agent/key.pem

./eightly-agent --data /var/lib/eightly-agent
```

### Generating a Custom Self-Signed Cert

To generate a self-signed certificate with additional SANs (e.g., your LAN hostname):

```bash
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout key.pem -out cert.pem \
  -days 3650 \
  -subj "/CN=eightly-agent" \
  -addext "subjectAltName=DNS:localhost,DNS:docker-host-01.local,IP:192.0.2.50,IP:127.0.0.1"
```

This creates a cert valid for 10 years with your host's DNS name and IP in the SAN.

## Certificate Rotation

The agent loads the TLS certificate once at startup. To rotate certificates:

1. Replace `cert.pem` and `key.pem` in the data directory.
2. Restart the agent.

There is no hot-reload mechanism. The agent must be restarted to pick up new certificates.

For Docker deployments:

```bash
docker restart eightly-agent
```

For systemd deployments:

```bash
sudo systemctl restart eightly-agent
```

## TOFU (Trust On First Use) with EOS

Eight.ly OS uses a Trust On First Use model for agent TLS certificates:

1. When EOS first connects to a new agent, it retrieves the agent's TLS certificate.
2. EOS stores the certificate fingerprint.
3. On subsequent connections, EOS verifies that the agent presents the same certificate.
4. If the certificate changes unexpectedly, EOS flags the connection as potentially compromised.

This model works with self-signed certificates because trust is established through the fingerprint, not through a certificate authority chain.

### What This Means in Practice

- The agent's self-signed cert is fine for production use with EOS.
- Do not regenerate the agent's certificates unnecessarily -- this will break the TOFU trust and require re-acceptance in EOS.
- If you do need to regenerate (e.g., key compromise), delete both `cert.pem` and `key.pem` and restart the agent. Then re-accept the new fingerprint in EOS.

## Outbound TLS (Registration and Heartbeat)

Outbound TLS to EOS is **fail-closed by default**. `--server-url` must use `https`. On the first successful registration the agent records the server certificate's public key fingerprint in its state file, and every later heartbeat and re-registration must present the same key, so a swapped certificate is refused. This works with a self-signed EOS server and needs no CA.

| Flag | Env Var | Effect |
|------|---------|--------|
| `--server-ca <path>` | `EIGHTLY_SERVER_CA` | Also verify the server's certificate chain against this CA. The first-use pin still applies. |
| `--insecure-server-tls` | -- (flag only) | Turn off verification **and** pinning and allow a non-`https` URL. Dev only. |

`--insecure-server-tls` has no environment variable, so an inherited variable can never switch it on.

Trust between EOS and the agent is additionally established through the shared secret and enrollment token (see [Authentication](Authentication.md) and [Registration and Heartbeat](Registration-and-Heartbeat.md)).

## Server Configuration

The HTTPS server is configured with these timeouts:

| Setting | Value |
|---------|-------|
| ReadHeaderTimeout | 10 seconds |
| ReadTimeout | 30 seconds |
| WriteTimeout | 30 minutes |
| IdleTimeout | 120 seconds |
| MaxHeaderBytes | 1 MiB |

The long write timeout accommodates streaming Docker API responses (logs, events). WebSocket console sessions have their own limits (see [WebSocket Console](WebSocket-Console.md)).

## Verifying TLS

Check the agent's certificate:

```bash
# View certificate details
openssl s_client -connect localhost:9001 -showcerts </dev/null 2>/dev/null | openssl x509 -text -noout

# Check expiration
openssl s_client -connect localhost:9001 </dev/null 2>/dev/null | openssl x509 -enddate -noout
```

## See Also

- [Configuration](Configuration.md) -- `--data` flag that controls cert location
- [Installation](Installation.md) -- Custom cert mount examples
- [Registration and Heartbeat](Registration-and-Heartbeat.md) -- Outbound TLS configuration for registration and heartbeat
- [Troubleshooting](Troubleshooting.md) -- TLS-related errors
