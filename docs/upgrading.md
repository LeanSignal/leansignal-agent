# Upgrading

The LeanSignal Agent install is **three independent pieces** with separate
lifecycles:

| Piece | What it is | Upgraded by |
|-------|-----------|-------------|
| **agent binary** | the OpenTelemetry Collector (`leansignal-agent`) | the common upgrade |
| **VictoriaMetrics binary** | the co-located local metrics store (`victoria-metrics`) | occasional, opt-in |
| **VM data directory** | your on-disk metrics — the thing you must not lose | **never** touched by an upgrade |

The agent and VictoriaMetrics run as **two separate services** (systemd units,
launchd daemons, or Windows services) and VM's data lives in a **fixed directory
outside the binaries**:

| Platform | VM data directory (`--storageDataPath`) |
|----------|------------------------------------------|
| Linux | `/var/lib/leansignal-agent/vm` |
| macOS | `/usr/local/var/leansignal-agent/vm` |
| Windows | `%ProgramData%\LeanSignal\Agent\vm` |
| Kubernetes | the `victoria-metrics-single` PVC |

Because of this, **upgrading the agent never touches VM or its data** — that's the
default and the common case (e.g. `v0.1.0` → `v0.1.1`). Upgrading VictoriaMetrics
is a separate, opt-in operation that preserves the data directory.

> This page is about the **agent's local VictoriaMetrics** (the full-fidelity edge
> store next to the agent). The central **dataplane** VM is a managed service and
> is not upgraded from here.

---

## Agent-only upgrade (the default)

Swaps just the collector binary and restarts only the agent service. VictoriaMetrics
keeps running the whole time; **no metrics are lost**. The scripts back up the old
binary and roll back automatically if the new agent doesn't come back healthy.

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
Bump the chart's `appVersion` (the agent image) and upgrade — the bundled VM
StatefulSet + PVC are untouched:
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

---

## What survives an upgrade

| Item | Agent-only upgrade | VM upgrade (`--with-vm`) |
|------|--------------------|--------------------------|
| VM metric data | ✅ untouched (VM never stops) | ✅ retained (same data path) |
| `config.yaml` | ✅ kept (never overwritten) | ✅ kept |
| `agent.env` / service env | ✅ kept | ✅ kept |
| Service unit files | ✅ kept (not reinstalled) | ✅ kept |

> The **installer** (`install.sh`/`install.ps1`) rewrites service units and the env
> file; the **upgrader** deliberately does not, so operator customizations to units
> survive. If a release changes a service definition, the release notes will say to
> re-run the installer (which keeps your `config.yaml`, dropping a `config.yaml.new`).

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

Each GitHub release also ships a **`VERSIONS.txt`** listing exactly what it bundles:
```
agent=v0.1.0
victoria-metrics=1.111.0
```

---

## Which release artifact does what

This is why a release ships three sets of archives:

| Artifact | Purpose |
|----------|---------|
| `leansignal-agent-bundle_*` | **fresh install** (agent + VM together) — used by `install.sh`/`install.ps1` |
| `leansignal-agent_*` | **agent-only upgrade** payload — used by `upgrade.sh`/`upgrade.ps1` |
| `victoria-metrics-*` | **VM-only upgrade** payload (mirrored, pinned) — used by `--with-vm` |
| `VERSIONS.txt` | what the release bundles (agent + VM versions) |
| `checksums.txt`, `bundle-checksums.txt` | integrity for the above |
