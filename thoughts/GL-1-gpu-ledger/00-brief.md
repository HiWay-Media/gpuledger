# GL-1 — Brief: a ledger of who holds which GPU on a Nomad node

**Repository:** https://github.com/hiway-media/gpuledger · **Ticket:** GL-1 in `BACKLOG.md`
**Date:** 2026-09-24 · **Author:** Allan Nava (HiWay Media), with Claude

## Goal

Nomad's NVIDIA device plugin fingerprints and schedules GPUs; nothing shows who actually
holds a card. On HiWay's video farm four services share two GPUs per host — three under
Nomad, one started by hand with `docker run` — and the weekly check says whether a
worker answers, not whether the GPU works or who is on it. gpuledger is one static
binary per node that joins the driver's view, the container runtime and the local
Nomad agent per GPU, and reports the join as a table, as findings with a verdict, and
as Prometheus metrics. Read-only; nothing that could carry a secret is ever printed.

## Done when

- On a GPU node: `gpuledger ls` shows, per card, model, utilisation, memory, temperature,
  encoder sessions, the Nomad allocation it is reserved for, and the tenants that hold
  it, each marked reserved, unmanaged or unreserved; `gpuledger check` prints findings
  worst first with exit 0 by default; `gpuledger serve` exposes `/metrics`.
- The three columns compared on a real node against `nomad alloc status`, `nvidia-smi`
  and `docker ps`, mismatches explained, dated in the README.
- A Nomad system job spec deploys it fleet-wide from a release binary; Prometheus
  scrapes it through Consul.
- `go test ./...` runs without a GPU; CI builds static linux/amd64 and arm64.

## In scope

- Collectors: nvidia-smi CSV (GPUs, processes), containers via cgroup and the Docker
  API with Nomad labels, Nomad reservations via the local agent.
- The join, the nine findings, the exporter, the CLI, the system job, the docs.

## Out of scope

- Writing anything: no scheduling, no eviction, no `docker`/`nomad` command that changes
  state.
- Kubernetes, MIG partitions, non-NVIDIA GPUs.
- A cluster-wide UI: v0.2.0 reads every node's `/ledger`; for now, one node at a time.
- Binding NVML: shelling out to nvidia-smi keeps the binary static; revisit if the spawn
  ever matters.

## Constraints

- Standard library only; static binary; Node only for the repository's tooling.
- The Nomad ACL token comes from an environment variable named by flag; process names
  are binaries only; the only environment variable read from a container is
  `NVIDIA_VISIBLE_DEVICES`; metrics labels never carry a process, pid, image or path.
- A failing source is a finding, never a crash.
- Nomad at HiWay is 1.7.3 (out of support): the allocation API fields used must exist
  there; verify before the first run.

## Decisions taken

- **Reserved is Nomad's word, tenant is the driver's** — the findings name every
  combination rather than collapsing them.
- **Exit 0 by default** (checkfleet's contract); `--exit-on` for gates.
- **raw_exec system job**, because the ledger reads the host's driver, `/proc` and socket.

## Assumptions (proceeding this way unless corrected)

- 8 encoder sessions and 85 °C as default thresholds; both flags.
- A container holding a GPU with no process at this instant is a tenant (a worker
  between jobs), not an idle card.
- The `com.hashicorp.nomad.allocation_id` label is present on every Nomad container;
  job and task names only with `extra_labels`, otherwise resolved through the reservation.

## Open risks

- **Encoder-only sessions**: NVENC sessions without a CUDA context may be counted per GPU
  but not attributed to a process; `nvidia-smi encodersessions` is the candidate third
  source (GL-11).
- **Device id forms**: the plugin's short `GPU-xxxxxxxx` versus the full UUID versus an
  index; a mismatch turns a reserved tenant into a false BAD. The matching is forgiving
  and tested, but the first real run is what proves it.
- **Nomad 1.7.3**: `AllocatedResources.Tasks.*.Devices` must be present in the node
  allocations response on that version.
- **`gpub` is not inspectable** (no SSH key): the fleet run will have a hole until it is.
