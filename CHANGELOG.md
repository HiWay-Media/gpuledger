# Changelog

All notable changes to gpuledger. The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [SemVer](https://semver.org/). Items reference their `GL-n` backlog id.

## [Unreleased]

### Fixed
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

### Added
- Unit tests for every fix above, the Docker client over a unix socket, nvidia-smi's
  failure paths, the exit code under every policy and the HTTP handlers.

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
