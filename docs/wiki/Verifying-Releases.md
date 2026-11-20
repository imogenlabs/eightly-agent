---
description: "Check release checksums, signatures, build provenance, and SBOM attestations."
---

# Verifying Releases

Every release file is signed with [Sigstore cosign](https://sigstore.dev) by the release workflow, and every image by the Docker publish workflow. Each release also carries `SHA256SUMS` and an SPDX SBOM (`sbom.spdx.json`).

### Check a download against the checksums

```bash
sha256sum --ignore-missing -c SHA256SUMS
```

Every signature is made by a workflow running at the release tag, so the certificate names that workflow file at that exact tag. Set `TAG` to the release you downloaded:

```bash
TAG=v2.1.0
```

### Verify a binary

```bash
cosign verify-blob \
  --certificate eightly-agent-linux-amd64.crt \
  --signature eightly-agent-linux-amd64.sig \
  --certificate-identity "https://github.com/imogenlabs/eightly-agent/.github/workflows/release.yml@refs/tags/$TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  eightly-agent-linux-amd64
```

### Verify a container image

```bash
cosign verify \
  --certificate-identity "https://github.com/imogenlabs/eightly-agent/.github/workflows/docker-publish.yml@refs/tags/$TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "ghcr.io/imogenlabs/eightly-agent:${TAG#v}"
```

`latest`, `MAJOR.MINOR` and `MAJOR` point at the image of the release they were last moved by, so verify them with that release's tag. Images built from `main` (`:main` and `:<sha>`) are development builds, signed by `docker-publish.yml@refs/heads/main`.

The exact identity accepts only signatures made by this repo's own workflow at that tag, not by a fork, a similarly named repo or another branch.

### Verify build provenance and the SBOM attestation

GitHub artifact attestations are made for releases published after the repository went public; earlier releases have the cosign signatures and `sbom.spdx.json` only.

```bash
gh attestation verify eightly-agent-linux-amd64 --owner imogenlabs
gh attestation verify eightly-agent-linux-amd64 --owner imogenlabs \
  --predicate-type https://spdx.dev/Document
```
