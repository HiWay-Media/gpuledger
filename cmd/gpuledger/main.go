// gpuledger — who holds which GPU on a Nomad node, and what it is doing.
//
//	gpuledger ls        one row per GPU: model, utilisation, memory, encoder sessions,
//	                    who Nomad reserved it for, who actually holds it
//	gpuledger check     findings, worst first: unmanaged or unreserved tenants, contention,
//	                    saturated encoders, hot cards, reserved-but-idle, idle capacity
//	gpuledger serve     HTTP: /metrics (Prometheus), /ledger (JSON), /findings (JSON), /healthz
//	gpuledger version
//
// Flags (every subcommand):
//
//	--nomad-addr       http://127.0.0.1:4646     --nomad-token-env NOMAD_TOKEN (name of the variable)
//	--docker           unix:///var/run/docker.sock (or http://host:port)
//	--nvidia-smi       nvidia-smi                --proc /proc
//	--node             node name (default: hostname)
//	--json             machine-readable output for ls and check
//	--exit-on          "", warn, bad, error — check's exit code policy (default: always 0)
//	--encoder-max 8    --temp-max 85    --allow-unmanaged    --no-idle
//	--listen :9877     --interval 15s   (serve)
//
// Reads only. Never prints a command line, an environment value or a path from a
// container; the Nomad token comes from an environment variable named by flag, so it is
// neither on the command line nor in the process list.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hiway-media/gpuledger/internal/containers"
	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/metrics"
	"github.com/hiway-media/gpuledger/internal/nomad"
	"github.com/hiway-media/gpuledger/internal/nvidia"
	"github.com/hiway-media/gpuledger/internal/render"
	"github.com/hiway-media/gpuledger/internal/version"
)

type options struct {
	nomadAddr, tokenEnv, docker, smi, proc, node, exitOn, listen string
	jsonOut, allowUnmanaged, noIdle, noNomad, noDocker           bool
	encoderMax, tempMax                                          int
	interval                                                     time.Duration
}

func parse(args []string) (string, options, error) {
	fs := flag.NewFlagSet("gpuledger", flag.ContinueOnError)
	var o options
	fs.StringVar(&o.nomadAddr, "nomad-addr", envOr("NOMAD_ADDR", "http://127.0.0.1:4646"), "Nomad agent address")
	fs.StringVar(&o.tokenEnv, "nomad-token-env", "NOMAD_TOKEN", "name of the environment variable holding the Nomad ACL token")
	fs.StringVar(&o.docker, "docker", envOr("DOCKER_HOST", "unix:///var/run/docker.sock"), "Docker endpoint (unix:// or http://)")
	fs.StringVar(&o.smi, "nvidia-smi", "nvidia-smi", "nvidia-smi binary")
	fs.StringVar(&o.proc, "proc", "/proc", "procfs root, for pid → container resolution")
	fs.StringVar(&o.node, "node", "", "node name (default: hostname)")
	fs.StringVar(&o.exitOn, "exit-on", "", "check exit code policy: warn|bad|error (default: always 0)")
	fs.StringVar(&o.listen, "listen", ":9877", "serve: listen address")
	fs.BoolVar(&o.jsonOut, "json", false, "JSON output")
	fs.BoolVar(&o.allowUnmanaged, "allow-unmanaged", false, "unmanaged tenants are WARN, not BAD")
	fs.BoolVar(&o.noIdle, "no-idle", false, "do not report idle GPUs as findings")
	fs.BoolVar(&o.noNomad, "no-nomad", false, "do not ask Nomad (no reservations)")
	fs.BoolVar(&o.noDocker, "no-docker", false, "do not ask Docker (tenants by pid only)")
	fs.IntVar(&o.encoderMax, "encoder-max", findings.Default.EncoderMax, "encoder sessions at or above which a GPU is saturated")
	fs.IntVar(&o.tempMax, "temp-max", findings.Default.TempMaxC, "temperature (°C) at or above which a GPU is hot")
	fs.DurationVar(&o.interval, "interval", 15*time.Second, "serve: refresh interval")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage()) }
	cmd := "ls"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return "", o, err
	}
	if o.node == "" {
		o.node, _ = os.Hostname()
	}
	return cmd, o, nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func usage() string {
	return `gpuledger — who holds which GPU on a Nomad node, and what it is doing.

  gpuledger ls        one row per GPU: reservations and tenants
  gpuledger check     findings, worst first (--json, --exit-on warn|bad|error)
  gpuledger serve     /metrics, /ledger, /findings, /healthz on --listen (default :9877)
  gpuledger version

Flags: --nomad-addr --nomad-token-env --docker --nvidia-smi --proc --node --json
       --exit-on --encoder-max --temp-max --allow-unmanaged --no-idle --no-nomad --no-docker
       --listen --interval
`
}

// collect reads the three sources once. A failing source is recorded, never fatal.
func collect(ctx context.Context, o options) ledger.Ledger {
	in := ledger.Inputs{Node: o.node, At: time.Now(), Containers: map[string]containers.Container{}}
	gpus, procs, err := nvidia.Query(ctx, nvidia.NewRunner(o.smi))
	if err != nil {
		in.Errors = append(in.Errors, err.Error())
	}
	in.GPUs, in.Processes = gpus, procs
	if !o.noDocker {
		dc := containers.NewClient(o.docker)
		list, err := dc.List(ctx)
		if err != nil {
			in.Errors = append(in.Errors, "docker: "+err.Error())
		}
		for _, c := range list {
			in.Containers[c.ID] = c
		}
		in.ContainerOf = func(pid int) string {
			id := containers.ContainerIDOf(o.proc, pid)
			if id == "" {
				return ""
			}
			if _, ok := in.Containers[id]; !ok {
				if c, err := dc.Inspect(ctx, id); err == nil {
					in.Containers[id] = c
				}
			}
			return id
		}
	}
	if !o.noNomad {
		nc := nomad.NewClient(o.nomadAddr, o.tokenEnv)
		nodeID, err := nc.NodeID(ctx)
		if err != nil {
			in.Errors = append(in.Errors, "nomad: "+err.Error())
		} else if nodeID == "" {
			in.Errors = append(in.Errors, "nomad: the agent at "+o.nomadAddr+" is not a client node")
		} else if res, err := nc.Reservations(ctx, nodeID); err != nil {
			in.Errors = append(in.Errors, "nomad: "+err.Error())
		} else {
			in.Reservations = res
		}
	}
	return ledger.Build(in)
}

func policy(o options) findings.Policy {
	return findings.Policy{EncoderMax: o.encoderMax, TempMaxC: o.tempMax, AllowUnmanaged: o.allowUnmanaged, IdleIsFinding: !o.noIdle}
}

func main() {
	cmd, o, err := parse(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	ctx := context.Background()
	switch cmd {
	case "version":
		fmt.Println("gpuledger", version.Version)
	case "ls":
		l := collect(ctx, o)
		if o.jsonOut {
			json.NewEncoder(os.Stdout).Encode(l)
		} else {
			fmt.Println(render.Ledger(l))
			for _, e := range l.Errors {
				fmt.Fprintln(os.Stderr, "gpuledger:", e)
			}
		}
	case "check":
		l := collect(ctx, o)
		fs := findings.Evaluate(l, policy(o))
		if o.jsonOut {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"node": l.Node, "at": l.At, "findings": fs, "worst": findings.Worst(fs)})
		} else {
			fmt.Print(render.Findings(fs))
		}
		os.Exit(findings.ExitCode(fs, o.exitOn))
	case "serve":
		serve(ctx, o)
	case "help", "-h", "--help":
		fmt.Print(usage())
	default:
		fmt.Fprintf(os.Stderr, "gpuledger: unknown command %q\n%s", cmd, usage())
		os.Exit(2)
	}
}

func serve(ctx context.Context, o options) {
	var mu sync.RWMutex
	current := collect(ctx, o)
	go func() {
		t := time.NewTicker(o.interval)
		defer t.Stop()
		for range t.C {
			l := collect(ctx, o)
			mu.Lock()
			current = l
			mu.Unlock()
		}
	}()
	snap := func() ledger.Ledger {
		mu.RLock()
		defer mu.RUnlock()
		return current
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, metrics.Render(snap()))
	})
	mux.HandleFunc("/ledger", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap())
	})
	mux.HandleFunc("/findings", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		l := snap()
		fs := findings.Evaluate(l, policy(o))
		json.NewEncoder(w).Encode(map[string]any{"node": l.Node, "at": l.At, "findings": fs, "worst": findings.Worst(fs)})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if len(snap().Errors) > 0 {
			w.WriteHeader(503)
		}
		fmt.Fprintln(w, "ok")
	})
	fmt.Fprintf(os.Stderr, "gpuledger %s serving on %s (refresh %s)\n", version.Version, o.listen, o.interval)
	if err := http.ListenAndServe(o.listen, mux); err != nil {
		fmt.Fprintln(os.Stderr, "gpuledger:", err)
		os.Exit(1)
	}
}
