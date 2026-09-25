<p align="center"><img src="https://raw.githubusercontent.com/hiway-media/gpuledger/main/assets/logo.svg" width="96" height="96" alt="gpuledger"></p>

# gpuledger — who holds which GPU on your Nomad cluster, and what it is doing

Nomad's NVIDIA device plugin fingerprints GPUs and schedules them. It does not tell you
who is actually on a card: the allocation Nomad gave it to, the container somebody
started by hand with `--gpus`, the bare process that survived a deploy, the worker
that holds a device and does nothing. gpuledger is one static binary that asks the
driver, the container runtime and the local Nomad agent, joins the three answers per
GPU, and reports them as a table, as findings with a verdict, and as Prometheus metrics.
It is **read-only**, dependency-free, and it never prints a command line or an
environment value.

```
$ gpuledger ls
gpuledger · gpud · 2 GPU(s) · 2026-09-24 09:12:40Z
│ gpu │ model           │ util │ memory        │ temp  │ enc │ reserved by (nomad)            │ tenants                                                                                     │
├─────┼─────────────────┼──────┼───────────────┼───────┼─────┼────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────┤
│ 0   │ Quadro RTX 4000 │ 72%  │ 3120/8192 MiB │ 66 °C │ 3   │ gpu-gpud-restreamer/restreamer │ docker:gpu-d-new-c0 (2048 MiB) !unmanaged; nomad:gpu-gpud-restreamer/restreamer (1024 MiB) │
│ 1   │ Quadro RTX 4000 │ 0%   │ 0/8192 MiB    │ 41 °C │ 0   │ tngrm-video-worker-gpud/worker │                                                                                             │

$ gpuledger check
🔴 BAD   unmanaged-tenant   gpu0 GPU-fef8089b   container gpu-d-new-c0 (encoding:handbrake-4.1.3-prod) holds this GPU outside Nomad — 2048 MiB, pids [4242]
🟡 WARN  contended          gpu0 GPU-fef8089b   2 tenants share this GPU: [gpu-d-new-c0 restreamer-3e5d2f75-abcd]
🟡 WARN  reserved-idle      gpu1 GPU-ac81e44d   reserved by tngrm-video-worker-gpud/worker (alloc 77777777) but no process or container holds it

3 findings: 0 OK, 2 WARN, 1 BAD, 0 ERROR
```

> **Status: the Nomad side is tested against real Nomad agents, every stable minor from
> 1.0 to 2.0; the NVIDIA side against a fake `nvidia-smi`.** CI starts each agent with
> ACLs on and a device plugin that fingerprints fake `nvidia/gpu` devices, schedules real
> Docker tasks onto them and checks every case of the join (see
> [Compatibility](#compatibility)). What no CI can do is the run on a GPU node with the
> real driver and the real device plugin: that comparison is GL-10 in `BACKLOG.md`, and
> until it is in this README, treat the driver side as documented, not observed. Built
> for the HiWay Media video farm, where four services share two GPUs per host and one of
> them is started by hand.

## What it answers

| Question | Where the answer comes from |
|---|---|
| What is on each card right now — utilisation, memory, temperature, power, NVENC sessions | `nvidia-smi --query-gpu`, the documented CSV form, no NVML binding |
| Which processes hold it, and how much memory each | `nvidia-smi --query-compute-apps` — the process name is reduced to its binary, arguments are never read |
| Which container each process is in, and whether Nomad started it | `/proc/<pid>/cgroup` → container id → Docker Engine API; Nomad's `com.hashicorp.nomad.alloc_id` label (plus `job_name`, `task_name`, `namespace` when the driver's `extra_labels` are on) |
| Which containers hold a GPU with no process at this instant | the container's `NVIDIA_VISIBLE_DEVICES` and `DeviceRequests` — the only environment variable it reads |
| Which allocation Nomad **reserved** each GPU for | the local agent: `/v1/agent/self` for the node id, `/v1/node/<id>/allocations` for `AllocatedResources.Tasks.*.Devices[].DeviceIDs` |

The join is per GPU: the reservations Nomad made, the tenants that actually hold it, and
whether each tenant was reserved. From that, the findings:

| Code | Level | Meaning |
|---|---|---|
| `unreserved-tenant` | BAD | a Nomad task uses a GPU Nomad did not allocate to it — a `NVIDIA_VISIBLE_DEVICES=all` or a missing `device` stanza |
| `unmanaged-tenant` | BAD (WARN with `--allow-unmanaged`) | a container nobody orchestrates, or a bare host process, holds the card |
| `contended` | WARN | two or more distinct tenants on one GPU |
| `reserved-idle` | WARN | Nomad reserved the GPU and nothing holds it |
| `encoder-saturated` | WARN | NVENC sessions at or above `--encoder-max` (default 8) |
| `hot` | WARN | temperature at or above `--temp-max` (default 85 °C) |
| `source-unavailable` | ERROR | nvidia-smi, Docker or Nomad could not be read, or Nomad did not return the allocation of a Nomad container (the token lacks `read-job` on its namespace) — the ledger is partial and says so |
| `idle` | OK | no tenant, no reservation: free capacity (`--no-idle` hides it) |
| `held` | OK | every tenant on the card is the one Nomad reserved it for |

Worst first. The exit code is 0 whatever the findings — a check that ran is a success —
unless `--exit-on warn|bad|error` asks for one, for CI and for check runners.

## Run it

**Once, on a node:**

```
gpuledger ls
gpuledger check --json
gpuledger check --exit-on bad --allow-unmanaged
```

**As a system job**, one per GPU node, scraping `/metrics` on 9877:
[`deploy/nomad/gpuledger.nomad.hcl`](deploy/nomad/gpuledger.nomad.hcl). It uses
`raw_exec` on purpose: the ledger reads the host's `nvidia-smi`, `/proc` and Docker
socket, which a container would have to be handed anyway.

```
nomad job run -var version=0.1.0 deploy/nomad/gpuledger.nomad.hcl
curl -s http://<node>:9877/metrics | grep gpuledger_gpu_tenants
```

Metrics: `gpuledger_up`, `gpuledger_gpu_info{model,bus}`, `_utilization_percent`,
`_memory_used_bytes`, `_memory_total_bytes`, `_temperature_celsius`, `_power_watts`,
`_encoder_sessions`, `_tenants`, `_reservations`, `gpuledger_gpu_state{state}` and, with
a history, `gpuledger_gpu_state_since_timestamp_seconds{state}` — alert on
`time() - gpuledger_gpu_state_since_timestamp_seconds{state="reserved-idle"} > 6*3600` —
and per tenant
`gpuledger_tenant_memory_bytes{kind,container,container_id,job,task,alloc,namespace}` and
`gpuledger_tenant_reserved`. Labels carry names and ids, never a process name, a pid, an
image or a path. `/ledger` and `/findings` return the same as JSON; `/healthz` is 503,
with the failing sources in the body, while a source is unreadable.

**Since when.** With `--history FILE`, `serve` remembers each GPU's state — free,
reserved-idle, held or unaccounted — and when it entered it, so `reserved-idle` says
*for 6h12m*, the number a scheduling decision needs. One record per GPU, no tenant in
it; written atomically each refresh. `ls` and `check` with the same flag read the file
and never write it. A silence longer than three refresh intervals, or a refresh where a
source failed, restarts the clock rather than claim a continuity nobody saw. The system
job keeps the file in its allocation's data dir on a sticky disk, so it survives a
deploy on the same node.

**The fleet**, from anywhere that reaches the nodes: `gpuledger fleet` reads every
node's `/ledger` — found through Consul, where the system job registers as `gpuledger`,
or listed with `--targets` — and counts each GPU as exactly one of held, reserved-idle,
unaccounted (a tenant Nomad did not reserve the card for, or cannot vouch for) or free,
per node and per job. `fleet check` evaluates every node with one policy, worst first; a
node that cannot be read is a `source-unavailable` ERROR, not a failure of the command.

```
gpuledger fleet --consul consul.service:8500
gpuledger fleet ls --targets gpua:9877,gpub:9877 --json
gpuledger fleet check --exit-on bad
```

Illustrative output, the shape `fleet ls` prints:

```
gpuledger fleet · 7 node(s), 1 unreachable · 12 GPU(s)
│ node  │ gpus │ held │ reserved-idle │ unaccounted │ free │ memory              │
├───────┼──────┼──────┼───────────────┼─────────────┼──────┼─────────────────────┤
│ gpua  │ 2    │ 1    │ 1             │ 0           │ 0    │ 2048/16384 MiB      │
│ gpud  │ 2    │ 0    │ 1             │ 1           │ 0    │ 3120/16384 MiB      │
│ …     │      │      │               │             │      │                     │
│ gpug  │ —    │ —    │ —             │ —           │ —    │ unreachable: …      │
│ fleet │ 12   │ 5    │ 3             │ 2           │ 2    │ 21504/98304 MiB     │

│ job                          │ reserved │ held │
├──────────────────────────────┼──────────┼──────┤
│ default/gpu-gpud-restreamer  │ 1        │ 1    │
│ default/tngrm-video-worker   │ 3        │ 1    │
```

It asks the nodes' gpuledger only, never their Nomad, Docker or driver: each node is
seen exactly as its own agent sees it. `--consul` defaults to `$CONSUL_HTTP_ADDR`, the
Consul token comes from the variable named by `--consul-token-env` (`CONSUL_HTTP_TOKEN`),
`--consul-service gpuledger`, `--timeout 5s` per node.

**Flags:** `--nomad-addr` (default `$NOMAD_ADDR` or `http://127.0.0.1:4646`),
`--nomad-token-env NOMAD_TOKEN` — the **name** of the variable holding the ACL token, so
the token is never on a command line — `--docker unix:///var/run/docker.sock`,
`--nvidia-smi`, `--proc /proc`, `--node`, `--json`, `--no-nomad`, `--no-docker`,
`--encoder-max`, `--temp-max`, `--allow-unmanaged`, `--no-idle`, `--listen`, `--interval`,
`--history`;
for `fleet`, `--targets`, `--consul`, `--consul-service`, `--consul-token-env`, `--timeout`.

**With ACLs**, the token needs [`deploy/nomad/gpuledger.policy.hcl`](deploy/nomad/gpuledger.policy.hcl):
`agent:read`, `node:read` and `read-job` on the namespaces that run GPU jobs. The last
one is easy to miss: without it Nomad answers the node's allocations **without** that
namespace's, and no error — gpuledger reports `source-unavailable` for each Nomad
container whose allocation it cannot see rather than calling it unreserved.

**Turn on the docker driver's `extra_labels`** (`job_name`, `task_name`, `namespace`) on
GPU nodes: without them a Nomad tenant is known by its allocation id and container name
only.

## Compatibility

The [Nomad matrix](.github/workflows/nomad.yml) runs on every change and weekly: the
latest patch of every Nomad minor from 1.0, read from releases.hashicorp.com at run time,
plus 1.7.3. On each, `integration/nomad_test.go` starts `nomad agent -dev` with ACLs on
and Nomad's example device plugin rebuilt as vendor `nvidia`, type `gpu`, runs two
Docker jobs that ask for `device "nvidia/gpu"` — one in a second namespace — and a
container outside Nomad, and checks with the policy file's token that gpuledger says
`held`, `reserved-idle`, `unreserved-tenant`, `unmanaged-tenant` and `contended` on the
right cards; that a token without `read-job` or without `node:read` is an ERROR naming
it; that `/metrics` passes `promtool check metrics`; that the system job validates; and
that `fleet` counts the node's jobs right, directly and through a Consul dev agent's
health API with the `/healthz` check the system job declares.

| Nomad | Tested | Notes |
|---|---|---|
| 1.0 | ✓ | no `extra_labels` in the docker driver: tenants carry the allocation id only |
| 1.1 – 1.4 | ✓ | |
| 1.5 – 2.0 | ✓ | `datacenters = ["*"]` in the system job works from 1.5; before, pass `-var 'datacenters=[…]'` |
| 1.7.3 | ✓ | pinned: the version the HiWay farm runs |

What the matrix established, on every version: the docker driver labels containers
`com.hashicorp.nomad.alloc_id`; `/v1/node/<id>/allocations` filters by the token's
`read-job` without an error; the task's cgroup is `/system.slice/docker-<id>.scope`
or, on 1.3 – 1.6, `/nomad.slice/docker-<id>.scope`.

## What it does not do

- Write anything: no `nomad`, `docker` or `nvidia-smi` command that changes state is ever
  invoked. Reads only — the one file it writes is its own `--history`, when asked to.
- Bind NVML. Shelling out to `nvidia-smi`'s documented CSV queries keeps the binary
  static and the driver dependency out of the build; the cost is one process spawn per
  refresh, tens of milliseconds.
- See encoder-only sessions as processes. NVENC sessions without a CUDA context appear
  in `encoder.stats.sessionCount` but not always in `query-compute-apps`; the count is
  reported per GPU, the owner is attributed only when a process is visible.
- Know about MIG partitions or Kubernetes. Nomad and plain Docker; the fleet view reads
  the nodes' own ledgers, it does not replace them.

## Install

Static binaries for linux/amd64 and linux/arm64 on the
[releases page](https://github.com/hiway-media/gpuledger/releases), with checksums.
From source: `go install github.com/hiway-media/gpuledger/cmd/gpuledger@latest` (Go 1.27).
Requires `nvidia-smi` on the node and, for tenant resolution, read access to the Docker
socket and `/proc`.

## Prior art

- [nomad-device-nvidia](https://github.com/hashicorp/nomad-device-nvidia): the plugin that
  fingerprints and schedules; gpuledger reads what it scheduled and compares it with
  what runs.
- [dcgm-exporter](https://github.com/NVIDIA/dcgm-exporter): per-GPU telemetry with pod
  labels on Kubernetes; no Nomad, no notion of an unmanaged tenant.
- [checkfleet](https://github.com/Allan-Nava/checkfleet): the same findings contract —
  worst first, exit 0 by default — for a fleet of domain checks.

## License

MIT.
