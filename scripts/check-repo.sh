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
grep -q "gpuledger" deploy/nomad/gpuledger.nomad.hcl || fail "the Nomad job spec must run gpuledger"
grep -q 'type *= *"system"' deploy/nomad/gpuledger.nomad.hcl || fail "the Nomad job must be a system job"
[ -x testdata/fake-nvidia-smi.sh ] || fail "testdata/fake-nvidia-smi.sh must be executable"
echo "ok — repo invariants hold at $V"
