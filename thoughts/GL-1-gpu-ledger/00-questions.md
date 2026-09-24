# 00 · Questions — GL-1 A ledger of who holds which GPU on a Nomad node


The default assumption is what makes this phase non-blocking: work can proceed
without waiting for answers, and the assumptions are on the record.

---

## Ticket

**ID:** GL-1
**Link:** https://github.com/hiway-media/gpuledger/blob/main/BACKLOG.md
**Title:** A ledger of who holds which GPU on a Nomad node

**Goal.** On HiWay's video farm, each GPU host carries two NVIDIA cards shared by four
services — three run under Nomad, one started by hand with `docker run`. Nomad's NVIDIA
device plugin fingerprints and schedules the cards, but nothing shows who actually holds
one, and the weekly fleet check only says whether a worker answers, not whether its GPU
works or who is on it. `gpuledger` is one static binary per node that, for every GPU,
joins three views — the driver's (what the card is doing and which processes are on it),
the container runtime's (which container each process belongs to, and whether that
container is a Nomad one) and the local Nomad agent's (which allocation the card is
reserved for) — and reports the join three ways: a table (`gpuledger ls`), findings with a
verdict, worst first (`gpuledger check`), and Prometheus metrics (`gpuledger serve`,
`/metrics`).

**Done when.**

- On a GPU node, `ls` shows per card: model, utilisation, memory, temperature, encoder
  sessions, the Nomad allocation the card is reserved for, and the tenants holding it,
  each marked *reserved*, *unmanaged* or *unreserved*. `check` prints findings worst
  first and exits 0 by default. `serve` exposes `/metrics`.
- The three columns are compared on a real node against `nomad alloc status`,
  `nvidia-smi` and `docker ps`; mismatches explained and dated in the README.
- A Nomad system job spec deploys it fleet-wide from a release binary; Prometheus scrapes
  it through Consul.
- `go test ./...` passes without a GPU; CI builds static linux/amd64 and linux/arm64.

**In scope.** Three collectors (driver via `nvidia-smi` CSV output, containers via cgroup
plus the Docker API with Nomad labels, reservations via the local Nomad agent), the join,
nine findings, the exporter, the CLI, the system job, the docs.

**Constraints.**

- Go standard library only; static binary. Node.js only for repository tooling.
- Strictly read-only: no scheduling, no eviction, no `docker`/`nomad` command that
  changes state.
- Secret hygiene: the Nomad ACL token is read from an environment variable whose name is
  given by a flag; process names are reported as bare binaries; the only environment
  variable read out of a container is `NVIDIA_VISIBLE_DEVICES`; metric labels never carry
  a process name, pid, image or path.
- A failing source is a finding, never a crash.
- Nomad at HiWay is 1.7.3 (out of support): every allocation-API field relied on must
  exist in that version, verified before the first run.

**Decisions already taken.**

- *Reserved* is Nomad's word, *tenant* is the driver's; findings name every combination
  of the two rather than collapsing them.
- Exit 0 by default (the same contract as checkfleet); `--exit-on` turns findings into a
  gate.
- Deployed as a `raw_exec` system job, because the ledger reads the host's driver,
  `/proc` and sockets.
- Shell out to `nvidia-smi` rather than bind NVML, to keep the binary static.

**Working assumptions in the brief.** Default thresholds of 8 encoder sessions and 85 °C,
both flag-overridable. A container that holds a GPU but has no process on it at this
instant is a tenant (a worker between jobs), not an idle card. The
`com.hashicorp.nomad.allocation_id` label is on every Nomad container; job and task
names are on the container only when Nomad's `extra_labels` is set, otherwise they are
resolved through the reservation.

**Risks the brief names.** NVENC-only sessions with no CUDA context may be countable per
GPU but not attributable to a process (a third source is tracked as GL-11). Device ids
come in several forms (plugin short id, full UUID, index) and a mismatch turns a reserved
tenant into a false BAD. The Nomad 1.7.3 node-allocations response must actually carry
per-task device assignments. One node, `gpub`, cannot be inspected (no SSH key), so the
first fleet run has a hole.

---

## Questions

### Q1 · Does the Nomad 1.7.3 node-allocations response carry per-task GPU device assignments, and under which path?

- **Risk if unresolved:** The reservation column is the one thing no other tool on the
  node provides; it is the reason the ledger exists. If the field the design reads is
  absent, renamed or empty in 1.7.3 — or is present in `GET /v1/allocation/:id` but not in
  the cheaper `GET /v1/node/:id/allocations` — then every card reports *no reservation*,
  every Nomad tenant becomes an *unreserved* finding, and the real-node comparison in the
  README fails on its first line. Discovering this at Implement means re-entering Design
  for the whole Nomad collector and possibly adding a per-allocation fan-out with its own
  ACL and rate implications.
- **Default assumption:** The field exists and is populated: device assignments have been
  part of `AllocatedResources.Tasks[TASK].Devices[]` (vendor, type, name, `DeviceIDs`)
  since device plugins arrived in Nomad 0.9, and the node-allocations endpoint returns
  full `Allocation` objects that include `AllocatedResources`. Research confirms this
  against the 1.7.x API documentation or a live agent before Design commits to it.
- **Answer:** _(to be filled — human)_

### Q2 · Which device-id forms do the three sources emit for the same card, and what match rule counts as "the same GPU"?

- **Risk if unresolved:** The join key is the GPU identity. `nvidia-smi` prints a full
  `GPU-` followed by the full 36-character UUID, the Nomad device plugin may record a short `GPU-xxxxxxxx` or the full
  UUID, `NVIDIA_VISIBLE_DEVICES` may hold an index (`0`, `1`), a UUID, a short UUID or
  `all`, and `nvidia-smi --query-compute-apps` reports processes by `gpu_uuid` or
  `gpu_bus_id` depending on the field chosen. If the match rule is too strict, a properly
  reserved Nomad tenant is reported as *unreserved* (a false BAD, the worst failure for a
  tool that people will act on); too loose, and two cards collapse into one. Research
  cannot see a real node, so the forms must be stated up front or the tests will encode
  the wrong ones.
- **Default assumption:** Canonical identity is the full UUID from `nvidia-smi`. Matching
  is forgiving and ordered: exact UUID, then case-insensitive prefix match on the
  `GPU-` short form (unique prefix required, otherwise unmatched with a finding), then
  numeric index resolved through `nvidia-smi`'s own index column. `all` in
  `NVIDIA_VISIBLE_DEVICES` means every card on the host. The rule is unit-tested with
  fixtures for each form.
- **Answer:** _(to be filled — human)_

### Q3 · What exactly are the nine findings, and how does each map to a verdict level and to `--exit-on`?

- **Risk if unresolved:** The brief counts nine findings and names the principle
  ("every combination of reserved and tenant") but does not list them, nor the severity
  scale. Design will invent a set; if it is not the set the backlog author had in mind,
  `check` output disagrees with the README's stated contract, `--exit-on` gates the wrong
  conditions in CI or in checkfleet, and the "worst first" ordering has no agreed axis.
  Changing the finding set after the exporter ships also renames metrics that dashboards
  already query.
- **Default assumption:** Three levels, OK < WARN < BAD, and `--exit-on LEVEL` returns
  non-zero when any finding is at or above that level. The nine are: (1) reserved card
  with no tenant — WARN, idle reservation; (2) Nomad tenant on a card not reserved for
  its allocation — BAD; (3) unmanaged tenant (non-Nomad container or bare host process)
  on a reserved card — BAD; (4) unmanaged tenant on an unreserved card — WARN; (5) more
  than one Nomad allocation holding the same card — BAD; (6) encoder sessions above
  threshold — WARN; (7) temperature above threshold — WARN; (8) a source failed
  (`nvidia-smi`, Docker, Nomad — one finding each, three total, or one finding
  parameterised by source) — WARN, the card row shows what is known; (9) a device id that
  could not be matched to any card — WARN. If the human has a different nine, they
  replace this list; the structure (level, per-(GPU, tenant) granularity, stable finding
  codes) stands.
- **Answer:** _(to be filled — human)_

### Q4 · How is a container with `NVIDIA_VISIBLE_DEVICES=all` (or unset, with `--gpus all`) attributed — a tenant on every card, or only where it has a live process?

- **Risk if unresolved:** The hand-started `docker run` service is the whole motivating
  case and almost certainly runs with `--gpus all`, which sets `NVIDIA_VISIBLE_DEVICES=all`.
  Under the brief's own assumption ("a container holding a GPU with no process is a
  tenant") it becomes an *unmanaged* tenant on both cards, so both cards raise a BAD or
  WARN forever, including the card it never touches; the finding is then noise from the
  first run and people learn to ignore the tool. Under the opposite reading, the container
  is invisible until a process appears and the ledger under-reports exactly the tenant it
  was built to expose. Whichever way, this determines the shape of the *tenant* column and
  the tests for the container collector.
- **Default assumption:** Visibility and occupancy are reported separately. A container
  with `all` is listed as a tenant on every card, marked *unmanaged* (or *reserved* /
  *unreserved* for Nomad containers), with an *active* flag that is true only where the
  driver shows one of its processes on that card. The unmanaged-on-reserved-card finding
  fires on visibility (the container *could* take the card), because that is the
  operational risk; the verdict level for visibility-only is WARN, and BAD only when a
  live process is on the card. The `all` case is a fixture in the tests.
- **Answer:** _(to be filled — human)_

### Q5 · How does a GPU process get mapped to its container on HiWay's nodes — cgroup v1 or v2, which Docker socket, and is Docker the only runtime Nomad uses there?

- **Risk if unresolved:** Attribution goes pid → `/proc/PID/cgroup` → container id →
  Docker API → Nomad labels. The cgroup line format differs between v1
  (`.../docker/ID`) and v2 (`0::/system.slice/docker-ID.scope`), and Nomad's own
  cgroup layout adds a `nomad.slice` variant. If the parser matches only one layout, every
  process on the other layout falls through to *bare host process* and the Nomad tenants
  all look unmanaged. If the Docker socket is not at the default path, is not readable by
  the `raw_exec` job's user, or Nomad drives some tasks through `containerd`/`podman`
  instead, the container collector fails on every node and the ledger degrades to
  nvidia-smi plus reservations. A wrong guess here is not caught by `go test` without a
  GPU; it surfaces only on the real node.
- **Default assumption:** Nodes run Ubuntu with cgroup v2 (unified) and Docker as the
  only Nomad task driver besides `raw_exec`; the parser accepts both v1 and v2 layouts and
  the Nomad slice naming, with fixtures for each. Docker is reached at
  `unix:///var/run/docker.sock` (flag-overridable) using a pinned old API version
  (`v1.24`) and only `GET /containers/json` and `GET /containers/ID/json`. The system job
  runs as root, as `raw_exec` jobs do by default, so the socket and `/proc` are readable.
- **Answer:** _(to be filled — human)_

### Q6 · Which `nvidia-smi` and driver versions are on the fleet, and are the query fields the ledger needs (encoder session count, per-process GPU uuid) supported on all of them?

- **Risk if unresolved:** `nvidia-smi --query-gpu` rejects the whole query when any one
  field is unknown, so a single field that arrived after the fleet's driver version (the
  encoder-session count and `gpu_uuid` on `--query-compute-apps` are the candidates)
  turns the driver collector into a failing source on every node, and the ledger ships
  with an empty table. If `nvidia-smi` is not on the `raw_exec` job's PATH, the same.
  Because tests run without a GPU, only fixtures from the real versions catch this.
- **Default assumption:** Every node runs a datacentre driver of the 5xx series (well
  past 470), `nvidia-smi` is at `/usr/bin/nvidia-smi` (flag-overridable), and the
  fields `uuid,index,name,utilization.gpu,memory.used,memory.total,temperature.gpu,
  encoder.stats.sessionCount` plus `--query-compute-apps=pid,used_memory,gpu_uuid` are
  all accepted. Research captures real CSV output from one node as the test fixture, and
  the collector issues the two queries separately so an unsupported field in one does not
  blank the other.
- **Answer:** _(to be filled — human)_

### Q7 · How does the ledger reach the local Nomad agent — address, TLS, whether an ACL token is required for the node-allocations read, and how it learns its own node id?

- **Risk if unresolved:** If the agent HTTP API is on a non-default address, requires
  TLS client certificates, or enforces ACLs where the operator's token lacks
  `node:read` / `namespace:read-job` on the relevant namespaces, the Nomad collector
  fails and every card shows *no reservation*. Getting the node id wrong (host name vs
  Nomad node id vs node name) silently returns another node's allocations or none. This
  also fixes the flag surface (`--nomad-addr`, `--nomad-token-env`) that the system job
  spec and README must document, so a late change ripples into three deliverables.
- **Default assumption:** Plain HTTP on `http://127.0.0.1:4646` (flag-overridable, and
  `NOMAD_ADDR` honoured when set), node id from `GET /v1/agent/self`
  (`.stats.client.node_id`), then `GET /v1/node/ID/allocations` filtered to
  `ClientStatus == running`. The token is optional: read from the variable named by
  `--nomad-token-env` (default `NOMAD_TOKEN`) and sent as `X-Nomad-Token` only when
  non-empty; ACLs are enabled at HiWay and the system job carries a read-only policy
  token. No TLS on the loopback agent.
- **Answer:** _(to be filled — human)_

### Q8 · How does Prometheus at HiWay discover Consul services — which tag, meta key or naming convention makes a service scrapable, and on what port should the ledger listen?

- **Risk if unresolved:** "Prometheus scrapes it through Consul" is an acceptance
  criterion, but Consul service discovery is conventional: a `consul_sd_config` relabel
  rule typically keeps only services with a given tag (`prometheus`, `metrics`) or a meta
  key naming the path. A job that registers the service without that marker passes
  `nomad job status`, is healthy in Consul and is never scraped; nobody notices until the
  first dashboard is empty. The port matters too: a collision with another exporter on a
  shared static port breaks the system job on the nodes that already run it.
- **Default assumption:** The system job registers a Consul service named `gpuledger`
  with a TCP health check on a static port in the exporter range (`9500` unless Research
  finds the fleet's convention says otherwise), tagged the same way the fleet's other
  exporters are tagged; Research locates one existing exporter job spec and copies its
  registration block verbatim. Metrics are on `/metrics`, an operator-facing JSON ledger
  on `/ledger`, health on `/healthz`.
- **Answer:** _(to be filled — human)_

### Q9 · Which labels may appear on the metrics, and is the allocation id an acceptable label given its churn?

- **Risk if unresolved:** The brief forbids process, pid, image and path as labels but
  says nothing about the rest. Per-tenant metrics need something identifying the holder;
  `alloc_id` is precise but changes on every reschedule, so a busy node accumulates
  thousands of stale series over weeks and Prometheus memory grows with it. Job and task
  names are stable but are only on the container with `extra_labels`, otherwise they come
  from the reservation and are absent for unmanaged tenants. If the label set is decided
  in Implement it will be decided by convenience, and renaming labels after dashboards
  exist is a breaking change on a metric contract.
- **Default assumption:** Per-GPU gauges carry `gpu` (full UUID), `index` and `model`.
  Tenancy is one gauge per (GPU, holder) with `gpu`, `index`, `job`, `task`, `state`
  (`reserved` | `unmanaged` | `unreserved`) and `managed` (`nomad` | `docker` | `host`);
  `alloc_id` is omitted from series and lives in the `/ledger` JSON and the `ls` table
  instead. Unmanaged tenants get `job=""` and a stable `holder` label that is the container
  name (not image, not path); a bare host process gets `holder="host"`. Findings are one
  gauge with `code` and `level`, and a per-source `up` gauge with `source`.
- **Answer:** _(to be filled — human)_

### Q10 · Where does the release binary live, and how does the system job fetch it — GitHub Releases via Nomad's `artifact` stanza, or a HiWay-internal mirror?

- **Risk if unresolved:** The system job is a deliverable, and an `artifact` stanza needs
  a URL, a checksum and an arch selector that resolves per node (amd64 vs arm64). If the
  nodes have no egress to GitHub, or the fleet's convention is to pull from an internal
  bucket or bake binaries into an image, a job spec pointing at
  `github.com/hiway-media/gpuledger/releases/download/…` deploys nowhere. It also decides
  what CI must produce: release assets with a checksums file, versioned so the job spec
  can pin one.
- **Default assumption:** GitHub Actions builds `gpuledger_VERSION_linux_ARCH.tar.gz`
  for amd64 and arm64 on a `v*` tag, publishes them as GitHub Release assets with a
  `checksums.txt`, and the system job's `artifact` stanza downloads the matching arch by
  `${attr.cpu.arch}` with the checksum pinned. Nodes have egress to `github.com`. The
  repository is public or CI uses the default `GITHUB_TOKEN` only.
- **Answer:** _(to be filled — human)_

---

## Out of scope

Things the ticket might suggest but that we are **not** doing in this task:

- Any write path: no scheduling, no eviction, no draining, no `docker` or `nomad` command
  that changes state, no "fix it" flag on `check`.
- Kubernetes, MIG partitions, non-NVIDIA GPUs, Windows or macOS builds.
- Binding NVML (or any cgo): shelling out to `nvidia-smi` is the decision; revisited only
  if the spawn cost is measured to matter.
- Attributing NVENC-only encoder sessions to a process (`nvidia-smi encodersessions` as a
  third driver source): tracked as GL-11. GL-1 reports the per-GPU session count only.
- A cluster-wide view — an aggregator reading every node's `/ledger`, a web UI, a
  fleet-level `check`: planned for v0.2.0. GL-1 is one node at a time.
- Alerting rules, Grafana dashboards, recording rules and retention policy in Prometheus;
  the ledger exposes metrics, the observability stack is configured elsewhere.
- Historical storage of ledger snapshots, a database, or a diff between runs.
- Authentication or TLS on the ledger's own HTTP listener; it is a loopback/Consul-mesh
  exporter like the others.
- Inspecting `gpub`: the missing SSH key is an access problem, not a code problem; the
  first fleet run is documented with that hole.
- Replacing or changing checkfleet's weekly check; the ledger is a separate binary that
  checkfleet may later call.
- Packaging as `.deb`/`.rpm`, a Docker image of the ledger itself, or a Homebrew formula:
  the deliverable is a static binary plus a Nomad job spec.

---

## Status

- [x] Questions generated
- [ ] Reviewed by a human
- [ ] Answers collected (or assumptions explicitly accepted)

> Next phase: **Research**. The ticket is **not** passed to Research — only the
> questions and their answers.
