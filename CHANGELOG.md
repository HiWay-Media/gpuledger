# Changelog

All notable changes to gpuledger. The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [SemVer](https://semver.org/). Items reference their `GL-n` backlog id.

## [Unreleased]

### Added
- `--history FILE`: `serve` records each GPU's state and since when, atomically, each
  refresh; `ls` and `check` read it. `reserved-idle` and `idle` say for how long, `ls`
  has a `state` column, `/metrics` has `gpuledger_gpu_state{state}` and
  `gpuledger_gpu_state_since_timestamp_seconds`. A silence over three intervals, or a
  partial read, restarts the clock. The system job keeps the file on a sticky disk (GL-14).
- The ledger JSON has `state` and `stateSince` per GPU; `fleet` shares the one
  classification (`ledger.Classify`).
- `gpuledger fleet ls` and `fleet check`: every node's `/ledger`, from `--targets` or
  Consul's health API (`--consul`, default `$CONSUL_HTTP_ADDR`; the token from the variable
  named by `--consul-token-env`). Each GPU is counted as held, reserved-idle, unaccounted
  or free, per node and per job; `fleet check` evaluates every node with one policy. A
  node or a Consul that cannot be read is a `source-unavailable` ERROR (GL-13).
- The Nomad matrix runs `fleet` against the real node, directly and through a Consul dev
  agent with the system job's `/healthz` check.

## [0.1.0] — 2026-09-24

The first release. The Nomad side is tested against real agents on every stable minor
from 1.0 to 2.0; the NVIDIA side against a fake nvidia-smi until the run on a real GPU
node (GL-10, now in v0.2.0).

### Added
- The Nomad matrix: `integration/nomad_test.go` against a real `nomad agent -dev` on the
  latest patch of every Nomad minor since 1.0, read from releases.hashicorp.com at run
  time, plus 1.7.3 — ACLs on, Nomad's example device plugin rebuilt as `nvidia/gpu`,
  Docker tasks in two namespaces and one outside Nomad; every join case, `promtool check
  metrics` on `/metrics`, `nomad job validate` on the system job. On every change and
  weekly (GL-21). Green on 1.0.18, 1.1.18, 1.2.16, 1.3.16, 1.4.14, 1.5.17, 1.6.10, 1.7.3,
  1.7.7, 1.8.4, 1.9.7, 1.10.5, 1.11.3 and 2.0.7.
- `deploy/nomad/gpuledger.policy.hcl`: the ACL policy gpuledger's token needs —
  `agent:read`, `node:read`, `read-job` on the namespaces — the one the matrix tests with.
- The README's Compatibility section: what the matrix observed on every version.
- Unit tests for every fix below, the Docker client over a unix socket, nvidia-smi's
  failure paths, the exit code under every policy and the HTTP handlers.

### Changed
- Tenant series in `/metrics` carry a `container_id` label.
- The system job takes `datacenters` as a variable: `"*"` matches every datacenter from
  Nomad 1.5 only.
- The ledger JSON carries `nomadRead` and, per tenant, `allocVisible`.

### Fixed
- With ACLs, Nomad leaves out of `/v1/node/<id>/allocations`, without an error, the
  allocations in namespaces the token cannot `read-job`: every Nomad task there was a
  false `unreserved-tenant` BAD and its GPU a false `idle`. A Nomad container whose
  allocation was not returned is now a `source-unavailable` ERROR naming the namespace
  and the capability, and with Nomad unread (`--no-nomad`, or down) no task is judged on
  reservations (GL-22).
- A process name holding arguments leaked a path element from them (`ffmpeg -i
  /data/x/match.mp4` became `match.mp4`); the name is now cut at the first blank before
  the directory is dropped.
- `--exit-on` with an unknown level (`Bad`, `fatal`) meant "always exit 0" in a gate; it is
  now a usage error, as are `--interval` ≤ 0 (the ticker panicked) and arguments after
  the flags (`gpuledger --json check` ran `ls`).
- `/metrics` wrote each family's samples interleaved with the others'; families are now
  contiguous, HELP and TYPE first. Two tenants the Docker API could not name produced a
  duplicate series; tenant series carry `container_id`.
- Tenants holding a GPU without a process were ordered by map iteration; ties are now
  broken by container name and id.
- `docker inspect` of an id shorter than 12 characters panicked while formatting its error.
- Reservations keep NVIDIA GPUs only (vendor `nvidia`, type `gpu`): another plugin's
  device with type `gpu` could not match any UUID. A Nomad 403 names the ACL capability
  the token lacks.
- `/healthz` answered `ok` in the body of a 503; it now lists the failing sources.

## [0.0.1] — 2026-09-24

Not released: the first working binary, tested against fakes, before the QRSPI design
run and before the first run on a real node.

### Added
- Collectors: nvidia-smi CSV (GPUs and processes), containers via cgroup and the Docker
  Engine API with Nomad's labels, Nomad reservations from the local agent (GL-2, GL-3, GL-4).
- The ledger join: per GPU, reservations and tenants of kind nomad, docker or host, with
  the reserved flag (GL-5).
- Findings with the checkfleet contract — unreserved-tenant, unmanaged-tenant, contended,
  reserved-idle, encoder-saturated, hot, source-unavailable, idle, held — worst first,
  exit 0 unless `--exit-on` (GL-6).
- `ls`, `check`, `serve` with `/metrics`, `/ledger`, `/findings`, `/healthz`; `version` (GL-7).
- Fixtures, fake nvidia-smi, unit and end-to-end tests; CI with static builds; release by
  tag; the Nomad system job spec; the site from the README; the backlog with issue sync (GL-8, GL-9).
