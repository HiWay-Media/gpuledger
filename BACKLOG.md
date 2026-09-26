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

## v0.1.0 — One node, told the truth <!-- ms: phase=shipped -->

The first release. Its gate was the run on a real GPU node (GL-10); it became every
stable Nomad minor from 1.0 to 2.0, run for real in CI (GL-21), after a test-first pass
over the whole code (GL-22): the facts the collectors depend on are Nomad's, and the
matrix observes them on every version, weekly, where one node observes one. GL-10 still
owes the driver side and moved to v0.2.0, with GL-1 and GL-11.

- [x] **GL-2 — nvidia-smi collector**: `--query-gpu` and `--query-compute-apps` CSV,
  `[N/A]` tolerant, process names reduced to the binary. <!-- gl: prio=high size=S labels=collector ver=0.1.0 -->
- [x] **GL-3 — Container collector**: `/proc/<pid>/cgroup` (v1, v2, containerd) →
  Docker Engine API over the unix socket or http; only Nomad labels and
  `NVIDIA_VISIBLE_DEVICES` kept; containers holding a GPU without a process included.
  <!-- gl: prio=high size=M labels=collector ver=0.1.0 -->
- [x] **GL-4 — Nomad collector**: node id from the agent, reservations from the node's
  allocations' `AllocatedResources`, token from an env var named by flag.
  <!-- gl: prio=high size=S labels=collector ver=0.1.0 -->
- [x] **GL-5 — The join**: per GPU, reservations and tenants (nomad, docker, host) with
  the reserved flag; forgiving device id matching (UUID, short UUID, index, all).
  <!-- gl: prio=high size=M labels=ledger ver=0.1.0 -->
- [x] **GL-6 — Findings**: nine codes, worst first, `--exit-on`, `--allow-unmanaged`,
  `--no-idle`, thresholds by flag. <!-- gl: prio=high size=S labels=ledger ver=0.1.0 -->
- [x] **GL-7 — The CLI and the exporter**: `ls`, `check`, `serve` (`/metrics`, `/ledger`,
  `/findings`, `/healthz`), `version`; metrics labels never carry a process, pid, image or path.
  <!-- gl: prio=med size=M labels=enhancement ver=0.1.0 -->
- [x] **GL-8 — Tests without a GPU**: fixtures, `fake-nvidia-smi.sh`, fake Docker and Nomad
  servers, unit and end-to-end tests. <!-- gl: prio=high size=M labels=tests ver=0.1.0 -->
- [x] **GL-9 — Repo operating model**: CI (gofmt, vet, tests, static builds, check-repo),
  release by tag with checksums, drift check, Pages from README, backlog sync, the Nomad
  system job spec. <!-- gl: prio=med size=M labels=project,release ver=0.1.0 -->
- [x] **GL-12 — Release 0.1.0**: VERSION, CHANGELOG, tag — once the Nomad matrix is green
  on every version. <!-- gl: prio=med size=S labels=release ver=0.1.0 -->
- [x] **GL-21 — Nomad matrix**: `integration/nomad_test.go` against a real
  `nomad agent -dev` on the latest patch of every minor since 1.0 plus 1.7.3, weekly:
  ACLs on, Nomad's example device plugin rebuilt as `nvidia/gpu`, Docker tasks in two
  namespaces and one outside Nomad, every join case, the tested ACL policy file,
  `promtool check metrics`, `nomad job validate` of the system job. It found the silent
  `read-job` filter and the `alloc_id` label. <!-- gl: prio=high size=M labels=tests,collector ver=0.1.0 -->
- [x] **GL-22 — Test-first pass**: a failing test for each defect, then the fix — argument
  paths leaking into process names, `--exit-on` typos passing a gate, interleaved and
  duplicate metric series, map-ordered tenants, a panic on short container ids,
  allocations hidden by ACLs judged unreserved. <!-- gl: prio=high size=M labels=tests ver=0.1.0 -->

## v0.2.0 — The whole cluster, watched <!-- ms: phase=shipped -->

The cluster view, since-when, per-card thresholds and Podman — and, shipped with them
rather than a release later, what the v0.3.0 draft called "watched, not asked":
findings as metrics, alert rules, the dashboard and the JSON contract (GL-17 – GL-20).
What needs a real GPU node moved to v0.3.0.

- [x] **GL-13 — Cluster view**: one command that reads every node's `/ledger` via Consul
  service discovery (or a list of addresses) and prints the fleet: GPUs total, held,
  reserved-idle, unmanaged, per node and per job. `gpuledger fleet ls|check`.
  <!-- gl: prio=med size=M labels=enhancement ver=0.2.0 -->
- [x] **GL-14 — Capacity history**: a small on-disk record so `check` can say
  "idle for the last 6 h", the number a scheduling decision needs — one record per GPU
  (state, since, seen) rather than a ring of snapshots: `--history`.
  <!-- gl: prio=low size=M labels=ledger ver=0.2.0 -->
- [x] **GL-15 — Per-card thresholds**: encoder session limits and thermal limits by model
  (Quadro RTX 4000, L4, T4, A10) with the source of each number. Sessions from NVIDIA's
  support matrix (unrestricted on all four; the cap is GeForce's); thermal limits are
  not published per model, so they come from the driver — `temperature.gpu.tlimit` and
  the slowdown flags, as optional queries. <!-- gl: prio=low size=S labels=ledger,docs ver=0.2.0 -->
- [x] **GL-16 — Podman and containerd**: the cgroup already names them; the inspect side
  needs their APIs. Podman shipped — its Engine-compatible API, `libpod` cgroups, the
  allocation from the podman driver's container name; containerd split out as GL-23.
  <!-- gl: prio=low size=M labels=collector ver=0.2.0 -->
- [x] **GL-17 — Findings as metrics**: `gpuledger_findings{code,level}`, the count per
  code for each refresh, so an alert and `check` never disagree; labels carry the code
  and severity only, never a tenant. <!-- gl: prio=med size=S labels=enhancement ver=0.2.0 -->
- [x] **GL-18 — Alert rules**: `deploy/prometheus/gpuledger.rules.yml` covering
  `unreserved-tenant`, `source-unavailable` / `gpuledger_up == 0` and `reserved-idle`
  held with `for:`; `promtool test rules` in CI (a CI tool, not a Go dependency).
  <!-- gl: prio=med size=S labels=release,tests ver=0.2.0 -->
- [x] **GL-19 — Grafana dashboard**: `deploy/grafana/gpuledger.json` built only from
  gpuledger's own metrics: per node, per GPU, reserved against held, findings over time.
  <!-- gl: prio=low size=S labels=docs ver=0.2.0 -->
- [x] **GL-20 — Stable JSON**: a `schema` version field on `ls --json`, `check --json`,
  `/ledger` and `/findings`, golden-file tests, and a documented rule for what counts as
  a breaking change. <!-- gl: prio=med size=S labels=ledger,tests ver=0.2.0 -->

## v0.3.0 — Told by the hardware <!-- ms: phase=now -->

What CI cannot observe: the run on a GPU node with the real driver and device plugin,
the encoder sessions only a real `nvidia-smi` shows, and the design questions the
QRSPI run settles with those answers in hand.

- [ ] **GL-10 — First run on a real node**: `gpuledger check` on one host of the farm
  (`gpud` has both a Nomad restreamer and a hand-started encoding container) against
  `nomad alloc status`, `nvidia-smi` and `docker ps`; the three-column comparison and
  every mismatch into the README, dated — the only evidence for the driver side
  (nvidia-smi, the NVIDIA device plugin) that CI cannot produce.
  <!-- gl: prio=high size=S labels=benchmark -->
- [ ] **GL-11 — Encoder-only sessions**: `nvidia-smi encodersessions` lists NVENC
  sessions with their pids where `query-compute-apps` does not; parse it as a third
  source so an encoder tenant is attributed, not just counted. Blocked on GL-10: the
  bare-metal column layout is not published (researched 2026-09-25, only vGPU's
  `vgpu -es` is), so the parser needs the real output from a farm node.
  <!-- gl: prio=med size=M labels=collector -->
- [ ] **GL-1 — Run QRSPI on the brief: Questions → Research → Spec → Plan**: input
  `thoughts/GL-1-gpu-ledger/00-brief.md`, one fresh session per phase. The design
  questions it must settle: how encoder-only sessions (NVENC without a CUDA context) are
  attributed when `query-compute-apps` does not list them; whether a tenant seen by the
  driver but absent from Docker and Nomad (a bare process) should ever be OK; the
  thresholds' defaults per card family (Quadro RTX 4000, L4). (What the token needs
  with ACLs is answered by GL-21: `deploy/nomad/gpuledger.policy.hcl`.) <!-- gl: prio=high size=L labels=ledger -->
- [ ] **GL-23 — containerd**: tasks under Nomad's community containerd driver. The API
  is gRPC, which the standard library does not speak: either `ctr -n <ns> containers
  info` as a read-only runner (the nvidia-smi pattern) or a dependency with its reason in
  `CLAUDE.md`. Needs the driver's labels and cgroup layout observed first, as GL-16 did
  for Podman. <!-- gl: prio=low size=M labels=collector -->

## v0.4.0 — A hardened cluster <!-- ms: phase=next -->

gpuledger on a cluster locked down the way production should be: Nomad's API over
mutual TLS, no long-lived ACL token to hand out, no Consul required to find the nodes,
and release binaries whose origin can be verified. Each item is observed in the Nomad
matrix, on every version that has the feature.

- [x] **GL-24 — Nomad over mTLS**: `https://` agents with a CA and a client certificate —
  `--nomad-ca-cert`, `--nomad-client-cert`, `--nomad-client-key`, defaulting to
  `NOMAD_CACERT`, `NOMAD_CLIENT_CERT`, `NOMAD_CLIENT_KEY` and `NOMAD_TLS_SERVER_NAME` as
  the Nomad CLI reads them; paths only, never key material in a flag. The matrix runs an
  agent with `tls { http = true, verify_https_client = true }` from certificates it makes.
  <!-- gl: prio=med size=M labels=collector,tests ver=main -->
- [ ] **GL-25 — Workload identity instead of a token**: from Nomad 1.7 the system job can
  take `identity { env = true }` and a job-bound ACL policy, so no static token exists to
  leak or rotate. Establish in the matrix which of `agent:read`, `node:read` and
  `read-job` a workload-associated policy can carry, and ship the job variant that works,
  keeping the token path for older clusters. <!-- gl: prio=med size=M labels=collector,docs -->
- [x] **GL-26 — Fleet through Nomad's service discovery**: `fleet --nomad-service
  gpuledger` reads `/v1/service/<name>` (Nomad 1.3+), so a cluster without Consul finds
  its nodes. The system job leaves `provider` out — it would stop validating before 1.3 —
  and the README says where to set it. Tested on every matrix version from 1.3. <!-- gl: prio=med size=S labels=enhancement,tests ver=main -->
- [x] **GL-27 — Consul over TLS**: `fleet --consul https://…` with `CONSUL_CACERT`,
  `CONSUL_CLIENT_CERT`, `CONSUL_CLIENT_KEY`, as Consul's CLI reads them; the Consul dev
  agent in the matrix started with TLS. <!-- gl: prio=low size=S labels=enhancement,tests ver=main -->
- [ ] **GL-28 — Verifiable releases**: build provenance attestations on every release
  binary (`gh attestation verify`), the checksum pinned in the system job's `artifact`
  from the release's checksums file, and the README's install steps verifying both.
  <!-- gl: prio=med size=S labels=release,docs -->
