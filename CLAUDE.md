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
cmd/gpuledger/main.go        the CLI: ls · check · serve · fleet ls|check · version; flags; collect() joins the sources
internal/nvidia/             nvidia-smi runner and CSV parsers (GPUQuery, ProcessQuery are the exact field lists)
internal/containers/         cgroup → container id (docker, containerd, libpod); Engine API client for Docker
                             and Podman (unix socket or http); Nomad labels, else the alloc id from the name
internal/nomad/              agent self → node id → node allocations → GPU reservations; token from an env var by name
internal/ledger/             the pure join: Entry per GPU with Reservations, Tenants and State (Classify:
                             free|reserved-idle|held|unaccounted); Kind nomad|docker|host
internal/findings/           Policy, Evaluate (codes below), Worst, ExitCode
internal/history/            per-GPU state and since when; Observe skips partial ledgers, a gap > MaxGap
                             restarts the clock; Save is tmp + rename; only serve writes it
internal/fleet/              every node's /ledger (static targets or Consul health API), Summarise per node
                             and job, Findings with one policy; a node not read is a Node with Err
internal/cards/               per-card NVENC engines and session caps from NVIDIA's support matrix, each with
                             its source; exact nvidia-smi names ("NVIDIA A10" is not an A10G or an A100)
internal/testcerts/          a throwaway CA, server and client certificate for tests (unit and matrix);
                             imported by tests only
internal/metrics/            Prometheus text exposition, hand-written
internal/render/             the table and the findings text
internal/version/            Version, set by -ldflags at release
testdata/                    nvidia-smi CSV fixtures, procfs cgroup fixtures, fake-nvidia-smi.sh;
                             golden/ is the JSON contract's reference (ls, check, fleet ls)
integration/                 build tag `integration`: gpuledger against a real `nomad agent -dev` (ACLs,
                             fake nvidia/gpu device plugin, docker tasks, a second namespace, promtool)
deploy/prometheus/           alert rules on gpuledger_findings and the state gauges; promtool unit tests beside them
deploy/grafana/              the dashboard; internal/metrics/contract_test.go checks every metric and label
                             the rules and the dashboard name against what Render exposes
deploy/nomad/                the system job spec (raw_exec, artifact from the release) and the ACL
                             policy file the integration test gives gpuledger's token
scripts/check-repo.sh        the repo's invariants (VERSION ↔ CHANGELOG, README statements, job spec); CI runs it
scripts/backlog.mjs          lint · roadmap · check · issues — Node, tooling only (package.json is private)
site/build.mjs               generates site/dist/index.html FROM README.md
.github/workflows/           ci.yml (gofmt, vet, test, static builds, check-repo, backlog), nomad.yml (the
                             integration test on every stable Nomad minor since 1.0 + 1.7.3; weekly), release.yml (tag v*:
                             binaries + checksums + GitHub release + milestone), release-drift.yml (VERSION with
                             no tag for 2 h), pages.yml, backlog-issues.yml
VERSION                      the one version; CHANGELOG.md must have its section; the tag is v<VERSION>
BACKLOG.md / ROADMAP.md      single source of truth (GL-n ids) / generated view
```

## The rules the code encodes

1. **Read-only.** No command that changes state is ever invoked; the Docker socket is
   used for GET only; Nomad for GET only. The one file written is `--history`, by
   `serve` only.
2. **Never print what could carry a secret.** Process names are the binary alone
   (`nvidia.ParseProcesses`), the only environment variable read is
   `NVIDIA_VISIBLE_DEVICES`, container labels kept are `com.hashicorp.nomad.*` only,
   metrics labels never carry a process name, a pid, an image or a path — and are never
   named `job` or `instance`, which Prometheus renames on ingestion (the Nomad job is
   `nomad_job`). The Nomad token
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
7. **Unreserved needs proof.** A Nomad tenant is `unreserved-tenant` only when Nomad was
   read and returned its allocation without this GPU. Nomad filters the node's
   allocations by the token's `read-job` silently; an allocation it did not return is a
   `source-unavailable` ERROR naming the namespace, and with Nomad unread there is no
   verdict on reservations at all.
8. **The device id matching is forgiving on purpose** (`ledger.gpuMatches`): full UUID,
   the plugin's short `GPU-xxxxxxxx`, the index, or `all`. A false negative here turns a
   reserved tenant into an `unreserved-tenant` BAD; add a form only with a test.

## Facts the code depends on (dated — re-verify before every tag)

- **Nomad docker driver** — observed by the Nomad matrix on 1.0.18 – 2.0.7, 2026-09-24:
  every container it starts carries `com.hashicorp.nomad.alloc_id` (the docs page's
  `allocation_id` is not what the driver writes; both are read); `job_name`,
  `task_group_name`, `task_name`, `namespace`, `node_name` only when listed in
  `extra_labels`, which Nomad 1.0 does not have (the agent refuses the config). The
  task's cgroup is `/system.slice/docker-<id>.scope`, `/nomad.slice/docker-<id>.scope`
  on 1.3 – 1.6. Nomad names its pause container `nomad_init_<alloc>`.
- **Nomad podman driver** (nomad-driver-podman 0.6.5, observed on Nomad 1.0.18 – 2.0.7,
  2026-09-25): no labels at all unless `extra_labels` is set — then `alloc_id` and the
  listed ones, same names as the docker driver (source: driver.go); the container is named
  `<task>-<alloc id>`; its cgroup is `/nomad.slice/libpod-<id>.scope/container`; conmon
  sits in `libpod-conmon-<id>.scope`. An alloc id from the name counts only when Nomad
  returns that allocation.
- **Nomad over mTLS** (same runs, Nomad 1.0.18 – 2.0.7): `tls { http, rpc, ca_file,
  cert_file, key_file, verify_server_hostname, verify_https_client }` on a -dev agent
  with one certificate naming `server.global.nomad`, `client.global.nomad`, `localhost`
  and 127.0.0.1; a client without a certificate is refused at the handshake.
- **Nomad ACLs** (same runs): `/v1/agent/self` needs `agent:read`, `/v1/node/<id>/allocations`
  needs `node:read` and returns only the allocations in namespaces where the token has
  `read-job`, with no error. `deploy/nomad/gpuledger.policy.hcl` is the tested minimum.
  `datacenters = ["*"]` matches every datacenter from 1.5 only.
- **NVIDIA device plugin** (developer.hashicorp.com/nomad/plugins/devices/nvidia,
  2026-09-23): device ids are the GPU UUIDs, shown as `GPU-fef8089b` in `ignored_gpu_ids`
  examples; the task receives `NVIDIA_VISIBLE_DEVICES`; the job asks with
  `device "nvidia/gpu" { count = 1 }`.
- **Allocation API** (observed on every version in the matrix): `AllocatedResources.Tasks.<task>.Devices[]`
  with `Vendor`, `Type`, `Name`, `DeviceIDs`; `/v1/node/<id>/allocations` returns full
  allocations; `/v1/agent/self` → `stats.client.node_id` on a client.
- **nvidia-smi**: `--query-gpu` and `--query-compute-apps` with `--format=csv,noheader,nounits`;
  `[N/A]` and `[Not Supported]` appear as values and parse to 0; UUIDs are `GPU-<uuid>`.
- **Nomad service discovery** (observed on Nomad 1.3.16 – 2.0.7, 2026-09-26; absent
  before 1.3, `/v1/services` is a 404): a group service with `provider = "nomad"`
  registers `{ServiceName, Namespace, NodeID, AllocID, Address, Port}` at
  `/v1/service/<name>?namespace=…`; the policy file's `read-job` is enough to read it.
- **Consul health API** (observed with a Consul 2.0.4 dev agent in the Nomad matrix,
  2026-09-25): `GET /v1/health/service/<name>?passing=true` → `[{Node:{Node,Address},
  Service:{Address,Port}}]`; `Service.Address` empty means the node's; token in
  `X-Consul-Token`.
- **NVENC** (developer.nvidia.com/video-encode-decode-support-matrix — the page moved from
  …-gpu-support-matrix-new; read in a browser for 0.2.0, 2026-09-25): concurrent sessions
  "Unrestricted" on Quadro RTX 4000 (Turing, 7th gen), L4 (Ada, 8th), T4 (Turing, 7th),
  A10 (Ampere, 7th); GeForce 12 on every line (GTX 1080, RTX 4090, RTX 5090) — it was 8 in
  2024–25, re-check before each tag. NVENC engines: 1, 2, 1, 1.
- **Thermal** (docs.nvidia.com/deploy/nvidia-smi, man page, NVSentinel runbook,
  2026-09-25): datasheets give ambient ranges only; `temperature.gpu.tlimit` is the margin
  in °C to the card's slowdown temperature; the thermal slowdown flags are
  `clocks_event_reasons.{hw,sw}_thermal_slowdown`, `clocks_throttle_reasons.*` before the
  rename — the driver version of the rename (535) comes from a Telegraf PR only. Neither
  has been observed on the farm's driver: that is part of GL-10.
- **`nvidia-smi encodersessions`**: its bare-metal column layout is not published
  (only vGPU's `vgpu -es`); GL-11 needs the real output from a farm node.
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
go vet -tags integration ./integration     # the real-Nomad test runs in CI (Linux, root, Docker)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/gpuledger ./cmd/gpuledger
go run ./cmd/gpuledger ls --nvidia-smi testdata/fake-nvidia-smi.sh --proc testdata/proc --no-docker --no-nomad
npm run backlog && npm run build:site
```

On a real node: `gpuledger check` with `NOMAD_TOKEN` exported if the cluster has ACLs;
compare `reserved by` with `nomad alloc status`, and `tenants` with `nvidia-smi` and
`docker ps`. That comparison, written into the README with a date, is GL-10: the only
evidence for the driver side that CI cannot produce.

## Conventions

- **The JSON is a contract** (`ledger.Schema`, README: The JSON contract). A golden
  diff is a change consumers see: regenerate with `go test ./cmd/gpuledger -run Golden
  -update` only on purpose, and bump `ledger.Schema` unless the change only adds fields.

- BACKLOG.md first: every idea is a `GL-n` item; shipped items say `ver=`. Regenerate
  ROADMAP.md; `check` fails when it is stale.
- CHANGELOG under `[Unreleased]` in the same pull request as the change.
- Prose in English, British-leaning spelling, em-dashes, no marketing filler, no
  decorative emoji (the finding glyphs in the terminal output are functional).
- Standard library only. A dependency needs a reason written in this file.
