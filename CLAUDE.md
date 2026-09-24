# CLAUDE.md

Guidance for Claude Code when working in this repository.

## What this repo is

`gpuledger` is a **single static Go binary** that answers, on one Nomad node, who holds
which GPU: the driver's view (`nvidia-smi` CSV), the container behind each process
(`/proc/<pid>/cgroup` → Docker Engine API → Nomad's labels), and the allocation Nomad
reserved the device for (local agent API). It prints a table, findings with a verdict,
or Prometheus metrics. Read-only, no dependencies beyond the standard library, MIT,
published by the HiWay Media org and dogfooded on its video farm.

`thoughts/GL-1-gpu-ledger/00-brief.md` is the task definition; the QRSPI run's
artifacts sit beside it.

## Layout

```
cmd/gpuledger/main.go        the CLI: ls · check · serve · version; flags; collect() joins the sources
internal/nvidia/             nvidia-smi runner and CSV parsers (GPUQuery, ProcessQuery are the exact field lists)
internal/containers/         cgroup → container id; Docker Engine client (unix socket or http); Nomad labels
internal/nomad/              agent self → node id → node allocations → GPU reservations; token from an env var by name
internal/ledger/             the pure join: Entry per GPU with Reservations and Tenants; Kind nomad|docker|host
internal/findings/           Policy, Evaluate (codes below), Worst, ExitCode
internal/metrics/            Prometheus text exposition, hand-written
internal/render/             the table and the findings text
internal/version/            Version, set by -ldflags at release
testdata/                    nvidia-smi CSV fixtures, procfs cgroup fixtures, fake-nvidia-smi.sh
deploy/nomad/                the system job spec (raw_exec, artifact from the release)
scripts/check-repo.sh        the repo's invariants (VERSION ↔ CHANGELOG, README statements, job spec); CI runs it
scripts/backlog.mjs          lint · roadmap · check · issues — Node, tooling only (package.json is private)
site/build.mjs               generates site/dist/index.html FROM README.md
.github/workflows/           ci.yml (gofmt, vet, test, static builds, check-repo, backlog), release.yml (tag v*:
                             binaries + checksums + GitHub release + milestone), release-drift.yml (VERSION with
                             no tag for 2 h), pages.yml, backlog-issues.yml
VERSION                      the one version; CHANGELOG.md must have its section; the tag is v<VERSION>
BACKLOG.md / ROADMAP.md      single source of truth (GL-n ids) / generated view
```

## The rules the code encodes

1. **Read-only.** No command that changes state is ever invoked; the Docker socket is
   used for GET only; Nomad for GET only.
2. **Never print what could carry a secret.** Process names are the binary alone
   (`nvidia.ParseProcesses`), the only environment variable read is
   `NVIDIA_VISIBLE_DEVICES`, container labels kept are `com.hashicorp.nomad.*` only,
   metrics labels never carry a process name, a pid, an image or a path. The Nomad token
   is read from an environment variable **named** by flag, never passed as a value.
3. **A failing source is a finding, not a crash.** `collect()` records the error, the
   ledger is built from what was read, `source-unavailable` is ERROR, `gpuledger_up` is 0,
   `/healthz` is 503.
4. **The join is pure and tested without a GPU.** `internal/ledger` takes data in and
   gives data out; every collector has an injectable transport (Runner, http base URL,
   procfs root). `testdata/fake-nvidia-smi.sh` stands in for the driver end to end.
5. **Reserved is Nomad's word, tenant is the driver's.** A GPU can be reserved and idle,
   held and unreserved, or both; the findings name each case with its own code.
6. **Worst first, exit 0 by default.** The checkfleet contract: a check that ran is a
   success; `--exit-on` is the gate.
7. **The device id matching is forgiving on purpose** (`ledger.gpuMatches`): full UUID,
   the plugin's short `GPU-xxxxxxxx`, the index, or `all`. A false negative here turns a
   reserved tenant into an `unreserved-tenant` BAD; add a form only with a test.

## Facts the code depends on (dated — re-verify before every tag)

- **Nomad docker driver** (developer.hashicorp.com/nomad/docs/deploy/task-driver/docker,
  read 2026-09-23): every container it starts carries the label
  `com.hashicorp.nomad.allocation_id`; `job_name`, `job_id`, `task_group_name`,
  `task_name`, `namespace`, `node_name`, `node_id` only when listed in `extra_labels`.
  Nomad names its pause container `nomad_init_<alloc>`.
- **NVIDIA device plugin** (developer.hashicorp.com/nomad/plugins/devices/nvidia,
  2026-09-23): device ids are the GPU UUIDs, shown as `GPU-fef8089b` in `ignored_gpu_ids`
  examples; the task receives `NVIDIA_VISIBLE_DEVICES`; the job asks with
  `device "nvidia/gpu" { count = 1 }`.
- **Allocation API**: `AllocatedResources.Tasks.<task>.Devices[]` with `Vendor`, `Type`,
  `Name`, `DeviceIDs`; `/v1/node/<id>/allocations` returns full allocations;
  `/v1/agent/self` → `stats.client.node_id` on a client.
- **nvidia-smi**: `--query-gpu` and `--query-compute-apps` with `--format=csv,noheader,nounits`;
  `[N/A]` and `[Not Supported]` appear as values and parse to 0; UUIDs are `GPU-<uuid>`.
- **Docker Engine API**: `GET /containers/json`, `GET /containers/<id>/json`
  (`Config.Labels`, `Config.Env`, `HostConfig.DeviceRequests`).
- **cgroup paths**: v2 `0::/system.slice/docker-<id>.scope`, v1 `…/docker/<id>`,
  containerd `containerd-<id>.scope`; the 64-hex id is what is matched.
- **HiWay's farm** (devops_hiway `docs/infrastructure/encoding-gpu`, 2026-07-15): 7 hosts
  `gpua`–`gpug` with 2× Quadro RTX 4000, four co-located services (restreamer, liveclip,
  video-worker on Nomad; encoding by hand), a rebuilt Cologno set with L4 passthrough.

## Verifying a change

```bash
gofmt -l cmd internal && go vet ./... && go test ./... -count=1
./scripts/check-repo.sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/gpuledger ./cmd/gpuledger
go run ./cmd/gpuledger ls --nvidia-smi testdata/fake-nvidia-smi.sh --proc testdata/proc --no-docker --no-nomad
npm run backlog && npm run build:site
```

On a real node: `gpuledger check` with `NOMAD_TOKEN` exported if the cluster has ACLs;
compare `reserved by` with `nomad alloc status`, and `tenants` with `nvidia-smi` and
`docker ps`. That comparison, written into the README with a date, is the 0.1.0 gate.

## Conventions

- BACKLOG.md first: every idea is a `GL-n` item; shipped items say `ver=`. Regenerate
  ROADMAP.md; `check` fails when it is stale.
- CHANGELOG under `[Unreleased]` in the same pull request as the change.
- Prose in English, British-leaning spelling, em-dashes, no marketing filler, no
  decorative emoji (the finding glyphs in the terminal output are functional).
- Standard library only. A dependency needs a reason written in this file.
