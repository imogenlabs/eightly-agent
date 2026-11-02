# SBOM — regeneration

A CycloneDX SBOM (`compliance/sbom.cdx.json`, CycloneDX spec 1.6) is committed to
this directory. Regenerate it from the **repo root** with:

```bash
go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest \
  mod -json -output compliance/sbom.cdx.json
```

Notes:
- The `WRN failed to determine version of main module ("git: reference not
  found")` line is benign — it only means the working tree has no tagged
  version; the dependency component graph is still complete and accurate.
- The release pipeline (`.github/workflows/release.yml`) independently emits an
  **SPDX** SBOM via `anchore/sbom-action` and attests it with
  `actions/attest-sbom`. The CycloneDX file here is the source-tree complement
  used for the compliance binder.
- Validate the output: `jq -e '.bomFormat == "CycloneDX"' compliance/sbom.cdx.json`.
