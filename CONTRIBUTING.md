# Contributing

## Local loop

```bash
go test ./... -count=1                  # unit tests + the CLI against fake nvidia-smi, Docker and Nomad
gofmt -l cmd internal && go vet ./...
./scripts/check-repo.sh                 # VERSION ↔ CHANGELOG, README statements, the job spec
go run ./cmd/gpuledger ls --nvidia-smi testdata/fake-nvidia-smi.sh --proc testdata/proc --no-docker --no-nomad
npm install && npm run backlog && npm run build:site   # tooling only: backlog lint, site from README
```

No GPU needed to develop: `testdata/fake-nvidia-smi.sh` answers the two CSV queries from
fixtures, and the tests start fake Docker and Nomad servers.

## Adding a finding, a metric or a source field

1. Confirm the shape on the documentation page or on a real node with a keys-only look;
   date the fact in `CLAUDE.md`.
2. Extend the fixtures (`testdata/`) and write the assertion first.
3. Keep rule 2: nothing that could carry a secret reaches the output.

## Verifying on a real node

```bash
gpuledger ls
gpuledger check --json | jq .worst
nomad alloc status <alloc>      # compare "reserved by"
nvidia-smi; docker ps           # compare "tenants"
```

Record one such comparison, dated, in the README before tagging 0.1.0.

## Backlog, roadmap, issues

`BACKLOG.md` is the single source of truth; `ROADMAP.md` is generated from it and the
GitHub issues are synced from it one way on every push to `main` that touches the file.
Items carry a stable `GL-n` id and `<!-- gl: prio= size= labels= [ver=] -->`.

## Pull requests

`main` is protected: pull request, green CI, no direct pushes. Conventional subject with
the `GL-n` id, a CHANGELOG line under `[Unreleased]`.

## Releasing

```bash
# bump VERSION; rename CHANGELOG's [Unreleased] to [x.y.z] — date and open a new [Unreleased];
# ver=main → ver=x.y.z in BACKLOG.md; regenerate the roadmap; land it by pull request
git checkout main && git pull
git tag v$(cat VERSION) && git push origin v$(cat VERSION)
```

`release.yml` verifies the tag against `VERSION`, runs the tests, builds static binaries
for linux/amd64 and linux/arm64 with the version baked in, attaches them with a
checksums file to the GitHub release (notes from the CHANGELOG section) and closes the
milestone whose title starts with `v<version>`. `release-drift.yml` fails when `main`
carries a VERSION with no tag for two hours. Re-run with
`gh workflow run Release -f tag=v<version>`.
