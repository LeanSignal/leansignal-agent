# Upgrading

A LeanSignal Agent install is the **agent binary plus its co-located telemetry
stores**, each with its own lifecycle:

| Piece | What it is | Upgraded by |
|-------|-----------|-------------|
| **agent binary** | the OpenTelemetry Collector (`leansignal-agent`) | the common upgrade |
| **co-located stores** | the local stores next to the agent — VictoriaMetrics (metrics), and on Linux also Loki (logs) + Tempo (traces) | VictoriaMetrics has an opt-in in-place upgrade; Loki/Tempo are left running untouched (see below) |
| **store data directories** | your on-disk telemetry — the thing you must not lose | **never** touched by an upgrade |

The agent and each store run as **separate services** (systemd units, launchd
daemons, or Windows services), and every store keeps its data in a **fixed
directory outside the binaries**:

| Store | Signal | Data directory (Linux) | Local retention |
|-------|--------|------------------------|-----------------|
| VictoriaMetrics | metrics | `/var/lib/leansignal-agent/vm` | ~1 day |
| Loki | logs | `/var/lib/leansignal-agent/loki` | ~1h window |
| Tempo | traces | `/var/lib/leansignal-agent/tempo` | ~1h window |

**Loki and Tempo are Linux-only** for now. On **macOS** and **Windows** only
VictoriaMetrics is co-located (data at `/usr/local/var/leansignal-agent/vm` and
`%ProgramData%\LeanSignal\Agent\vm` respectively); on **Kubernetes** it is the
`victoria-metrics-single` PVC.

Because of this, **upgrading the agent never stops the co-located stores or
touches their data** — that's the default and the common case (e.g. `v0.1.0` →
`v0.1.1`). Upgrading VictoriaMetrics is a separate, opt-in operation that
preserves its data directory.

> This page is about the **agent's local co-located stores** (the full-fidelity
> edge stores next to the agent). The central **dataplane** VM and the tenant
> Loki/Tempo are managed services and are not upgraded from here.

---

## Agent-only upgrade (the default)

Swaps just the collector binary and restarts only the agent service. The
co-located stores keep running the whole time; **no telemetry is lost**. The
scripts back up the old binary and roll back automatically if the new agent
doesn't come back healthy.

### Linux / macOS
```bash
# to the latest release
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.sh | sudo bash

# to a specific version
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.sh | sudo bash -s -- --version v0.2.0
```

### Windows (elevated PowerShell)
```powershell
iwr https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.ps1 -OutFile upgrade.ps1
.\upgrade.ps1                 # latest
.\upgrade.ps1 -Version v0.2.0 # specific version
```

### Kubernetes
Bump the chart's `appVersion` (the agent image) and upgrade — the bundled
VictoriaMetrics StatefulSet + PVC are untouched:
```bash
helm upgrade leansignal-agent oci://ghcr.io/leansignal/charts/leansignal-agent \
  --version <chart-version> --reuse-values
```

---

## Upgrading VictoriaMetrics too (opt-in, data-safe)

Only needed on releases that bump the bundled VM version, and only if you want it.
VictoriaMetrics single-node supports **in-place upgrades**: the binary is swapped
against the **same** `--storageDataPath`, so existing data is retained. Before
touching anything the scripts take an instant [VM snapshot](https://docs.victoriametrics.com/#how-to-work-with-snapshots)
(a hardlink copy under the data dir) and **abort the upgrade if it can't be
confirmed** — override with `--skip-snapshot` (`-SkipSnapshot` on Windows) only if
you knowingly accept the risk. On success the snapshot is removed automatically; if
the upgrade fails it is **kept** under `<data-dir>/vm/snapshots/` for manual recovery.

### Linux / macOS
```bash
# upgrade the agent AND VictoriaMetrics (to the VM version this release ships)
curl -fsSL .../upgrade.sh | sudo bash -s -- --with-vm

# pin an explicit VM version
curl -fsSL .../upgrade.sh | sudo bash -s -- --with-vm --vm-version 1.115.0
```

### Windows
```powershell
.\upgrade.ps1 -WithVM
.\upgrade.ps1 -WithVM -VmVersion 1.115.0
```

### Kubernetes
The bundled VM is a `victoria-metrics-single` subchart. Bump its pinned version in
your values (or the chart) and `helm upgrade`; the PVC is retained across the
StatefulSet rollout.

**Before bumping VM across a large version jump**, skim the
[VictoriaMetrics release notes](https://docs.victoriametrics.com/changelog/) for any
storage-format note. Downgrades are only safe within a compatible storage format —
prefer the snapshot (kept automatically by `--with-vm`) if you need to revert.

### Loki and Tempo (Linux)

`upgrade.sh` does **not** upgrade the co-located Loki or Tempo — there is no
`--with-loki` / `--with-tempo` flag. An agent upgrade (with or without
`--with-vm`) leaves both services **running untouched**, so logs and traces keep
flowing the whole time. Their versions are pinned by the `LOKI_VERSION`
(`3.5.12`) and `TEMPO_VERSION` (`2.7.1`) files and are pulled from the
grafana/loki and grafana/tempo GitHub releases **at install time** — only a fresh
install (or bumping the pin and re-running `install.sh`) re-pulls them. To move
Loki/Tempo to a new version, re-run the installer with `--loki-version` /
`--tempo-version`; it keeps their data directories and your existing
`loki.yaml` / `tempo.yaml` (dropping a `.new` alongside).

---

## What survives an upgrade

| Item | Agent-only upgrade | VM upgrade (`--with-vm`) |
|------|--------------------|--------------------------|
| VictoriaMetrics data | ✅ untouched (VM never stops) | ✅ retained (same data path) |
| Loki / Tempo data (Linux) | ✅ untouched (those services never stop) | ✅ untouched (`--with-vm` touches only VM) |
| `config.yaml` (+ `loki.yaml` / `tempo.yaml`) | ✅ kept (never overwritten) | ✅ kept |
| `agent.env` / service env | ✅ kept | ✅ kept |
| Service unit files | ✅ kept (not reinstalled) | ✅ kept |

> The **installer** (`install.sh`/`install.ps1`) rewrites service units and the env
> file; the **upgrader** deliberately does not, so operator customizations to units
> survive. If a release changes a service definition, the release notes will say to
> re-run the installer (which keeps your existing config files, dropping a
> `config.yaml.new` / `loki.yaml.new` / `tempo.yaml.new` alongside).

---

## Rollback

The scripts keep the previous binary next to the live one and restore it
automatically if the service fails its health check. To roll back manually:

```bash
# Linux / macOS — the previous binary is saved as *.prev only on failure;
# otherwise reinstall the older version explicitly:
curl -fsSL .../upgrade.sh | sudo bash -s -- --version v0.1.0
```
```powershell
.\upgrade.ps1 -Version v0.1.0
```
For VictoriaMetrics, the pre-upgrade **snapshot** under
`<data-dir>/vm/snapshots/` is a point-in-time copy you can restore per the VM docs.

---

## Checking versions

```bash
# agent version (works on all platforms)
leansignal-agent --version

# VictoriaMetrics version
victoria-metrics --version
# or, from the running store:
curl -s http://127.0.0.1:8428/metrics | grep vm_app_version
```
On Kubernetes: `kubectl exec deploy/leansignal-agent -- leansignal-agent --version`.

On Linux, the co-located Loki and Tempo run the versions pinned by the
`LOKI_VERSION` / `TEMPO_VERSION` files (currently `3.5.12` / `2.7.1`).

Each GitHub release also ships a **`VERSIONS.txt`** listing exactly what it ships:
```
agent=v0.2.0
victoria-metrics=1.111.0
loki=3.5.12
tempo=2.7.1
```

---

## Which release artifact does what

This is why a release ships three sets of archives:

| Artifact | Purpose |
|----------|---------|
| `leansignal-agent-bundle_*` | **fresh install** (agent + VictoriaMetrics together) — used by `install.sh`/`install.ps1` |
| `leansignal-agent_*` | **agent-only upgrade** payload — used by `upgrade.sh`/`upgrade.ps1` |
| `victoria-metrics-*` | **VM-only upgrade** payload (mirrored, pinned) — used by `--with-vm` |
| `VERSIONS.txt` | what the release ships (agent + VictoriaMetrics + the pinned Loki/Tempo versions) |
| `checksums.txt`, `bundle-checksums.txt` | integrity for the above |

**Loki and Tempo are not shipped as release artifacts.** On Linux, `install.sh`
downloads them from the grafana/loki and grafana/tempo GitHub releases at install
time (pinned by `VERSIONS.txt`); they have no dedicated upgrade payload, which is
why `upgrade.sh` leaves them running as-is.
