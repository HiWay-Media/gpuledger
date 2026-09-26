#!/bin/sh
# The repository's own invariants, run by CI and by hand. Exit 1 on the first failure.
set -eu
cd "$(dirname "$0")/.."
fail() { echo "✗ $1" >&2; exit 1; }
V="$(tr -d ' \n' < VERSION)"
echo "$V" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail "VERSION must be x.y.z, got '$V'"
grep -q "^## \[Unreleased\]" CHANGELOG.md || fail "CHANGELOG.md needs an [Unreleased] section"
grep -q "^## \[$V\]" CHANGELOG.md || fail "CHANGELOG.md has no section for $V"
for f in README.md CLAUDE.md CONTRIBUTING.md LICENSE BACKLOG.md ROADMAP.md CHANGELOG.md deploy/nomad/gpuledger.nomad.hcl; do [ -f "$f" ] || fail "$f is missing"; done
grep -qi "read.only" README.md || fail "README.md must state that gpuledger is read-only"
grep -qi "never prints\|never shows\|never carries" README.md || fail "README.md must state what is never printed (command lines, env values)"
grep -q "NOMAD_TOKEN" README.md || fail "README.md must say the token comes from an environment variable"
for job in deploy/nomad/gpuledger.nomad.hcl deploy/nomad/gpuledger.wi.nomad.hcl; do
  grep -q "gpuledger" "$job" || fail "$job must run gpuledger"
  grep -q 'type *= *"system"' "$job" || fail "$job must be a system job"
  grep -q 'checksum *= *var.checksum' "$job" || fail "$job must verify the artifact's checksum"
  grep -q 'nomad-node-id' "$job" || fail "$job must pass --nomad-node-id"
done
grep -q 'env *= *true' deploy/nomad/gpuledger.wi.nomad.hcl || fail "the workload identity job must set identity { env = true }"
grep -q 'attest-build-provenance' .github/workflows/release.yml || fail "the release must attest its binaries"
[ -x testdata/fake-nvidia-smi.sh ] || fail "testdata/fake-nvidia-smi.sh must be executable"
echo "ok — repo invariants hold at $V"
