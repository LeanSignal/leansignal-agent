# Releasing (maintainers)

Releases are automated by GitHub Actions + goreleaser, driven by a version tag.

## Cut a release

```bash
# 1. Ensure main is green and CHANGELOG is updated.
# 2. Tag and push:
git tag v0.1.0
git push origin v0.1.0
```

The [`release.yml`](../.github/workflows/release.yml) workflow then:

1. Installs OCB and runs `builder --config manifest.yaml --skip-compilation` to
   generate the distribution sources into `_build/`.
2. Runs **goreleaser**, which cross-compiles the binary for
   linux/macOS/windows × amd64/arm64, builds archives + checksums, builds and
   pushes multi-arch images to `ghcr.io/leansignal/leansignal-agent`, and creates
   the GitHub Release.
3. Runs [`scripts/release/build-bundles.sh`](../scripts/release/build-bundles.sh):
   downloads the pinned VictoriaMetrics binaries (`VM_VERSION`), verifies their
   checksums, **re-hosts** them on the release, and assembles the combined
   per-platform `leansignal-agent-bundle_*` archives (collector + VM + service
   scripts + config). **Only VictoriaMetrics is mirrored into the bundles** — Loki
   and Tempo are **not** bundled; `install.sh` pulls them from the `grafana/loki`
   and `grafana/tempo` GitHub releases at install time, pinned by `LOKI_VERSION` /
   `TEMPO_VERSION`. Each bundle carries a `VERSIONS.txt` recording all three pins.
   These archives are uploaded to the same release.
4. Packages the Helm chart and pushes it to `oci://ghcr.io/leansignal/charts`.

## Bumping VictoriaMetrics

1. Edit [`VM_VERSION`](../VM_VERSION).
2. Update the pinned checksums in
   [`scripts/release/vm-checksums.txt`](../scripts/release/vm-checksums.txt) for
   the new assets (the release runs `build-bundles.sh` with `STRICT_VM=1`, which
   fails if a checksum is missing).
3. Optionally bump the VM image tag in
   [`deploy/docker/docker-compose.yaml`](../deploy/docker/docker-compose.yaml)
   and the subchart version in the Helm `Chart.yaml`.

## Bumping Loki / Tempo

Loki and Tempo are pulled from upstream at install time — not mirrored into the
bundles — so bumping either is a one-line edit:

1. Edit [`LOKI_VERSION`](../LOKI_VERSION) and/or [`TEMPO_VERSION`](../TEMPO_VERSION).
2. Optionally bump the matching image tags in
   [`deploy/docker/docker-compose.yaml`](../deploy/docker/docker-compose.yaml).

`install.sh` reads these files (overridable with `--loki-version` /
`--tempo-version`) to fetch the pinned binaries from the grafana releases, and
the release records them in the bundle `VERSIONS.txt`.

## Local dry run

```bash
make generate                       # OCB sources into _build/
goreleaser release --snapshot --clean   # build everything locally, no publish
VERSION=0.0.0 DIST_DIR=dist ./scripts/release/build-bundles.sh
```

## Versioning

SemVer tags `vMAJOR.MINOR.PATCH` are the single source of truth. The release
workflow stamps the tag into everything:

- **Binary** — `dist.version` in `manifest.yaml` is overridden from the tag, so
  `leansignal-agent --version` reports the release version.
- **Archives, bundles, Docker tags** — named/tagged from the version by goreleaser
  and `build-bundles.sh` (images: `:<version>` and `:latest`).
- **Helm chart** — `version` and `appVersion` in `Chart.yaml` are set from the tag
  before packaging.

The committed `manifest.yaml` / `Chart.yaml` carry a baseline version for local
builds; CI overrides them per release. The agent version is independent of the
upstream OpenTelemetry Collector base version (pinned per-component in
`manifest.yaml`) and of the co-located store pins (`VM_VERSION`, `LOKI_VERSION`,
`TEMPO_VERSION`).
