# Changelog

All notable changes to gpuledger. The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [SemVer](https://semver.org/). Items reference their `GL-n` backlog id.

## [Unreleased]

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
