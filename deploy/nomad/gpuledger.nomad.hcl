# gpuledger as a Nomad system job: one instance per GPU node, exposing /metrics on
# port 9877 and registering in Consul for Prometheus to scrape. It reads nvidia-smi,
# the Docker socket (read-only) and the local Nomad agent; it writes nothing.
#
#   nomad job run deploy/nomad/gpuledger.nomad.hcl
#
# Pin the version and the checksum to a release before running this in production.

variable "version" {
  type    = string
  default = "0.1.0"
}

job "gpuledger" {
  type        = "system"
  datacenters = ["*"]
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
      # raw_exec, not docker: nvidia-smi and the host's /proc are what the ledger reads.
      driver = "raw_exec"

      artifact {
        source      = "https://github.com/hiway-media/gpuledger/releases/download/v${var.version}/gpuledger-v${var.version}-linux-amd64"
        destination = "local/gpuledger"
        mode        = "file"
        # options { checksum = "sha256:…" }  ← from the release's checksums file
      }

      config {
        command = "local/gpuledger"
        args    = ["serve", "--listen", "0.0.0.0:9877", "--interval", "15s", "--node", "${node.unique.name}"]
      }

      env {
        NOMAD_ADDR = "http://127.0.0.1:4646"
        # The ACL token, when the cluster has ACLs: name the variable, never inline it.
        # NOMAD_TOKEN is read from the environment by --nomad-token-env (default NOMAD_TOKEN).
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
