# Backlog — gpuledger

Single source of truth for what is planned. Items keep a stable `GL-n` id so commits,
the CHANGELOG, the `thoughts/` artifacts and the issues can reference them.

[ROADMAP.md](ROADMAP.md) is a **generated** view of this file — run
`node scripts/backlog.mjs roadmap` after touching it, or CI fails. The GitHub issues are
synced from it one way on every push to `main` that changes this file.

## How to write an item

```
## v0.2.0 — Title of the milestone <!-- ms: phase=next -->

- [ ] **GL-99 — Short name**: what it is, why it earns its place, what it needs to
  touch. <!-- gl: prio=high size=M labels=collector -->
```

- The **id never changes**; a new item takes the next free number.
- `- [ ]` open, `- [x]` shipped with `ver=x.y.z` (or `ver=main` when merged, unreleased);
  decided against → ticked with `ver=dropped` and the reason in the body.
- Labels: `collector`, `ledger`, `benchmark`, `release`, `docs`, `project`, `tests`, `enhancement`.

## v0.1.0 — One node, told the truth <!-- ms: phase=now -->

The first release: the ledger run on a real GPU node with the real device plugin, its
three columns compared against `nomad alloc status`, `nvidia-smi` and `docker ps`, and
the comparison written in the README with a date. Nothing about the collectors is
trusted until that comparison exists.

**The comparison is the gate on this milestone.** No `v0.1.0` before GL-10 is in the README.

- [ ] **GL-1 — Run QRSPI on the brief: Questions → Research → Spec → Plan**: input
  `thoughts/GL-1-gpu-ledger/00-brief.md`, one fresh session per phase. The design
  questions it must settle: how encoder-only sessions (NVENC without a CUDA context) are
  attributed when `query-compute-apps` does not list them; whether a tenant seen by the
  driver but absent from Docker and Nomad (a bare process) should ever be OK; the
  thresholds' defaults per card family (Quadro RTX 4000, L4); and what the system job
  needs on a node where Nomad has ACLs. <!-- gl: prio=high size=L labels=ledger -->
- [x] **GL-2 — nvidia-smi collector**: `--query-gpu` and `--query-compute-apps` CSV,
  `[N/A]` tolerant, process names reduced to the binary. <!-- gl: prio=high size=S labels=collector ver=main -->
- [x] **GL-3 — Container collector**: `/proc/<pid>/cgroup` (v1, v2, containerd) →
  Docker Engine API over the unix socket or http; only Nomad labels and
  `NVIDIA_VISIBLE_DEVICES` kept; containers holding a GPU without a process included.
  <!-- gl: prio=high size=M labels=collector ver=main -->
- [x] **GL-4 — Nomad collector**: node id from the agent, reservations from the node's
  allocations' `AllocatedResources`, token from an env var named by flag.
  <!-- gl: prio=high size=S labels=collector ver=main -->
- [x] **GL-5 — The join**: per GPU, reservations and tenants (nomad, docker, host) with
  the reserved flag; forgiving device id matching (UUID, short UUID, index, all).
  <!-- gl: prio=high size=M labels=ledger ver=main -->
- [x] **GL-6 — Findings**: nine codes, worst first, `--exit-on`, `--allow-unmanaged`,
  `--no-idle`, thresholds by flag. <!-- gl: prio=high size=S labels=ledger ver=main -->
- [x] **GL-7 — The CLI and the exporter**: `ls`, `check`, `serve` (`/metrics`, `/ledger`,
  `/findings`, `/healthz`), `version`; metrics labels never carry a process, pid, image or path.
  <!-- gl: prio=med size=M labels=enhancement ver=main -->
- [x] **GL-8 — Tests without a GPU**: fixtures, `fake-nvidia-smi.sh`, fake Docker and Nomad
  servers, unit and end-to-end tests. <!-- gl: prio=high size=M labels=tests ver=main -->
- [x] **GL-9 — Repo operating model**: CI (gofmt, vet, tests, static builds, check-repo),
  release by tag with checksums, drift check, Pages from README, backlog sync, the Nomad
  system job spec. <!-- gl: prio=med size=M labels=project,release ver=main -->
- [ ] **GL-10 — First run on a real node**: `gpuledger check` on one host of the farm
  (`gpud` has both a Nomad restreamer and a hand-started encoding container) against
  `nomad alloc status`, `nvidia-smi` and `docker ps`; the three-column comparison and
  every mismatch into the README, dated. **Gates the release.**
  <!-- gl: prio=high size=S labels=benchmark -->
- [ ] **GL-11 — Encoder-only sessions**: `nvidia-smi encodersessions` lists NVENC
  sessions with their pids where `query-compute-apps` does not; parse it as a third
  source so an encoder tenant is attributed, not just counted.
  <!-- gl: prio=med size=M labels=collector -->
- [ ] **GL-12 — Release 0.1.0**: VERSION, CHANGELOG, tag — after GL-10.
  <!-- gl: prio=med size=S labels=release -->

## v0.2.0 — The whole cluster <!-- ms: phase=next -->

- [ ] **GL-13 — Cluster view**: one command that reads every node's `/ledger` via Consul
  service discovery (or a list of addresses) and prints the fleet: GPUs total, held,
  reserved-idle, unmanaged, per node and per job. <!-- gl: prio=med size=M labels=enhancement -->
- [ ] **GL-14 — Capacity history**: a small on-disk ring of snapshots so `check` can say
  "idle for the last 6 h", the number a scheduling decision needs.
  <!-- gl: prio=low size=M labels=ledger -->
- [ ] **GL-15 — Per-card thresholds**: encoder session limits and thermal limits by model
  (Quadro RTX 4000, L4, T4, A10) with the source of each number.
  <!-- gl: prio=low size=S labels=ledger,docs -->
- [ ] **GL-16 — Podman and containerd**: the cgroup already names them; the inspect side
  needs their APIs. <!-- gl: prio=low size=M labels=collector -->
