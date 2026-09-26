# gpuledger as a Nomad system job, Nomad 1.5+, with no static token: the task's
# workload identity is its token (identity { env = true } sets NOMAD_TOKEN), and the ACL
# policy is bound to this job. The Nomad matrix runs this on every version that has
# job-bound policies (integration/nomad_test.go: TestWorkloadIdentity).
#
#   nomad acl policy apply -namespace default -job gpuledger \
#     -description "gpuledger: read-only GPU ledger" gpuledger-job deploy/nomad/gpuledger.policy.hcl
#   nomad job run -var version=V -var checksum=sha256:… deploy/nomad/gpuledger.wi.nomad.hcl
#
# --nomad-node-id is not optional here: before Nomad 1.11 the local agent's
# /v1/agent/self does not accept a workload identity (it answers 500).

variable "version" {
  type    = string
  default = "0.3.0"
}

# Required, no default: "sha256:<hex>" of gpuledger-v<version>-linux-amd64, from the
# release's gpuledger-v<version>-checksums.txt (README: Install).
variable "checksum" {
  type = string
}

# "*" means every datacenter from Nomad 1.5; before 1.5 it is a literal name that
# matches none, so name them: -var 'datacenters=["dc1"]'.
variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

job "gpuledger" {
  type        = "system"
  datacenters = var.datacenters
  namespace   = "default"

  # Only nodes that have GPUs — the device plugin fingerprints them.
  constraint {
    attribute = "${attr.driver.docker}"
    value     = "1"
  }
  constraint {
    attribute = "${attr.unique.hostname}"
    operator  = "regexp"
    value     = "gpu|cuda"
  }

  group "ledger" {
    # The state history (--history) lives in the allocation's data dir; sticky keeps it
    # across job updates on the same node, so "reserved-idle for 6h" survives a deploy.
    # No size: Nomad's default (300 MB) — it must exceed the task's log storage (100 MB).
    ephemeral_disk {
      sticky = true
    }

    network {
      port "http" {
        static = 9877
      }
    }

    service {
      name = "gpuledger"
      port = "http"
      tags = ["prometheus", "metrics"]
      check {
        type     = "http"
        path     = "/healthz"
        interval = "30s"
        timeout  = "5s"
      }
    }

    task "gpuledger" {
      identity {
        env = true
      }

      # raw_exec, not docker: nvidia-smi and the host's /proc are what the ledger reads.
      driver = "raw_exec"

      artifact {
        source      = "https://github.com/hiway-media/gpuledger/releases/download/v${var.version}/gpuledger-v${var.version}-linux-amd64"
        destination = "local/gpuledger"
        mode        = "file"
        # Nomad refuses a download that does not match: no unverified binary runs.
        options {
          checksum = var.checksum
        }
      }

      config {
        command = "local/gpuledger"
        args    = ["serve", "--listen", "0.0.0.0:9877", "--interval", "15s", "--node", "${node.unique.name}", "--nomad-node-id", "${node.unique.id}", "--history", "${NOMAD_ALLOC_DIR}/data/history.json"]
      }

      env {
        NOMAD_ADDR = "http://127.0.0.1:4646"
        # With the agent's API over TLS, the host's certificates by path, e.g.:
        #   NOMAD_ADDR        = "https://127.0.0.1:4646"
        #   NOMAD_CACERT      = "/etc/nomad.d/tls/ca.pem"
        #   NOMAD_CLIENT_CERT = "/etc/nomad.d/tls/cli.pem"
        #   NOMAD_CLIENT_KEY  = "/etc/nomad.d/tls/cli-key.pem"
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
