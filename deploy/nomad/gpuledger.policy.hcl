# The ACL policy gpuledger's token needs, and no more. The Nomad matrix in CI
# (integration/nomad_test.go) gives gpuledger a token with exactly this file, on every
# Nomad version it tests.
#
#   nomad acl policy apply -description "gpuledger: read-only GPU ledger" gpuledger deploy/nomad/gpuledger.policy.hcl
#   nomad acl token create -name gpuledger -policy gpuledger -type client
#
# agent:read  — /v1/agent/self, for the node id.
# node:read   — /v1/node/<id>/allocations.
# read-job    — without it Nomad leaves the namespace's allocations out of that list,
#               with no error; gpuledger then reports source-unavailable for every
#               Nomad container whose allocation it cannot see. Narrow "*" to the
#               namespaces that run GPU jobs if you prefer.

agent {
  policy = "read"
}

node {
  policy = "read"
}

namespace "*" {
  capabilities = ["read-job"]
}
