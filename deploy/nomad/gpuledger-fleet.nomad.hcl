# gpuledger fleet serve as a Nomad service job: one instance anywhere in the cluster
# reads every node's gpuledger and serves the fleet's page, /fleet and /metrics with the
# cluster's totals on port 9878. It reads the nodes' /ledger only — no Nomad, Docker
# or driver access of its own beyond what --discovery needs.
#
#   nomad job run -var version=V -var checksum=sha256:… deploy/nomad/gpuledger-fleet.nomad.hcl
#
# The Nomad matrix runs this job for real (integration/nomad_test.go).

variable "version" {
  type    = string
  default = "0.4.0"
}

# Required: "sha256:<hex>" of gpuledger-v<version>-linux-amd64 (README: Install).
variable "checksum" {
  type = string
}

# Where the binary comes from; empty is the release for var.version.
variable "artifact_url" {
  type    = string
  default = ""
}

variable "datacenters" {
  type    = list(string)
  default = ["*"]
}

variable "port" {
  type    = number
  default = 9878
}

# How fleet serve finds the nodes: Nomad's service discovery by default (Nomad 1.3+,
# the system job's service with provider = "nomad"); ["--consul", "127.0.0.1:8500"] or
# ["--targets", "gpua:9877,gpub:9877"] otherwise.
variable "discovery" {
  type    = list(string)
  default = ["--nomad-service", "gpuledger"]
}

job "gpuledger-fleet" {
  type        = "service"
  datacenters = var.datacenters
  namespace   = "default"

  group "fleet" {
    count = 1

    network {
      port "http" {
        static = var.port
      }
    }

    service {
      name = "gpuledger-fleet"
      port = "http"
      tags = ["prometheus", "metrics"]
      check {
        type     = "http"
        path     = "/healthz"
        interval = "30s"
        timeout  = "5s"
      }
    }

    task "fleet" {
      driver = "raw_exec"

      artifact {
        source      = var.artifact_url != "" ? var.artifact_url : "https://github.com/hiway-media/gpuledger/releases/download/v${var.version}/gpuledger-v${var.version}-linux-amd64"
        destination = "local/gpuledger"
        mode        = "file"
        options {
          checksum = var.checksum
        }
      }

      config {
        command = "local/gpuledger"
        args    = concat(["fleet", "serve", "--listen", "0.0.0.0:${var.port}", "--interval", "30s"], var.discovery)
      }

      env {
        NOMAD_ADDR = "http://127.0.0.1:4646"
        # With --nomad-service and ACLs: NOMAD_TOKEN (read-job on the namespace), by
        # variable name, never inline; with Consul, CONSUL_HTTP_TOKEN likewise.
      }

      resources {
        cpu    = 50
        memory = 48
      }
    }
  }
}
