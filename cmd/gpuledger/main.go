// gpuledger — who holds which GPU on a Nomad node, and what it is doing.
//
//	gpuledger ls        one row per GPU: model, utilisation, memory, encoder sessions,
//	                    who Nomad reserved it for, who actually holds it
//	gpuledger check     findings, worst first: unmanaged or unreserved tenants, contention,
//	                    saturated encoders, hot cards, reserved-but-idle, idle capacity
//	gpuledger serve     HTTP: /metrics (Prometheus), /ledger (JSON), /findings (JSON), /healthz
//	gpuledger fleet     every node's /ledger, from --targets or Consul: `fleet ls` counts GPUs
//	                    per node and per job, `fleet check` evaluates every node with one policy
//	gpuledger version
//
// Flags (every subcommand):
//
//	--nomad-addr       http://127.0.0.1:4646     --nomad-token-env NOMAD_TOKEN (name of the variable)
//	--nomad-ca-cert --nomad-ca-path --nomad-client-cert --nomad-client-key --nomad-tls-server-name
//	                   paths, defaulting to the Nomad CLI's NOMAD_CACERT, … variables
//	--docker           unix:///var/run/docker.sock (or http://host:port)
//	--podman           auto: unix:///run/podman/podman.sock when it exists; off; or an endpoint
//	--nvidia-smi       nvidia-smi                --proc /proc
//	--node             node name (default: hostname)
//	--json             machine-readable output for ls and check
//	--exit-on          "", warn, bad, error — check's exit code policy (default: always 0)
//	--encoder-max 0 (the card's cap)    --temp-max 85 (without a driver margin)    --allow-unmanaged    --no-idle
//	--listen :9877     --interval 15s   (serve)
//	--history FILE     per-GPU state and since when: serve writes it, ls and check read it
//	--targets h:p,…    --consul $CONSUL_HTTP_ADDR  --consul-service gpuledger
//	--consul-token-env CONSUL_HTTP_TOKEN (name of the variable)  --timeout 5s   (fleet)
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
	"github.com/hiway-media/gpuledger/internal/fleet"
	"github.com/hiway-media/gpuledger/internal/history"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/metrics"
	"github.com/hiway-media/gpuledger/internal/nomad"
	"github.com/hiway-media/gpuledger/internal/nvidia"
	"github.com/hiway-media/gpuledger/internal/render"
	"github.com/hiway-media/gpuledger/internal/version"
)

type options struct {
	nomadAddr, tokenEnv, docker, podman, smi, proc, node, exitOn, listen string
	jsonOut, allowUnmanaged, noIdle, noNomad, noDocker                   bool
	encoderMax, tempMax                                                  int
	interval                                                             time.Duration
	// fleet
	sub, targets, consul, consulService, consulTokenEnv string
	timeout                                             time.Duration
	history                                             string
	nomadTLS                                            nomad.TLS
}

func parse(args []string) (string, options, error) {
	fs := flag.NewFlagSet("gpuledger", flag.ContinueOnError)
	var o options
	fs.StringVar(&o.nomadAddr, "nomad-addr", envOr("NOMAD_ADDR", "http://127.0.0.1:4646"), "Nomad agent address")
	env := nomad.TLSFromEnv()
	fs.StringVar(&o.nomadTLS.CACert, "nomad-ca-cert", env.CACert, "CA certificate for an agent over TLS (default $NOMAD_CACERT)")
	fs.StringVar(&o.nomadTLS.CAPath, "nomad-ca-path", env.CAPath, "directory of CA certificates (default $NOMAD_CAPATH)")
	fs.StringVar(&o.nomadTLS.ClientCert, "nomad-client-cert", env.ClientCert, "client certificate, for verify_https_client (default $NOMAD_CLIENT_CERT)")
	fs.StringVar(&o.nomadTLS.ClientKey, "nomad-client-key", env.ClientKey, "the client certificate's key file (default $NOMAD_CLIENT_KEY)")
	fs.StringVar(&o.nomadTLS.ServerName, "nomad-tls-server-name", env.ServerName, "server name to verify, e.g. server.global.nomad (default $NOMAD_TLS_SERVER_NAME)")
	fs.StringVar(&o.tokenEnv, "nomad-token-env", "NOMAD_TOKEN", "name of the environment variable holding the Nomad ACL token")
	fs.StringVar(&o.docker, "docker", envOr("DOCKER_HOST", "unix:///var/run/docker.sock"), "Docker endpoint (unix:// or http://)")
	fs.StringVar(&o.podman, "podman", "auto", "Podman endpoint (its Docker-compatible API); auto: "+podmanSocket+" when it exists; off")
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
	fs.IntVar(&o.encoderMax, "encoder-max", findings.Default.EncoderMax, "encoder sessions at or above which a GPU is saturated; 0: the card's own cap (none on Quadro and datacenter cards), -1: off")
	fs.IntVar(&o.tempMax, "temp-max", findings.Default.TempMaxC, "temperature (°C) at or above which a GPU is hot, when the driver reports no thermal margin")
	fs.DurationVar(&o.interval, "interval", 15*time.Second, "serve: refresh interval")
	fs.StringVar(&o.history, "history", "", "state history file: serve writes it, ls and check read it")
	fs.StringVar(&o.targets, "targets", "", "fleet: gpuledger endpoints, host:port,…")
	fs.StringVar(&o.consul, "consul", envOr("CONSUL_HTTP_ADDR", ""), "fleet: Consul address, to discover the endpoints")
	fs.StringVar(&o.consulService, "consul-service", "gpuledger", "fleet: the Consul service gpuledger serve registers as")
	fs.StringVar(&o.consulTokenEnv, "consul-token-env", "CONSUL_HTTP_TOKEN", "fleet: name of the environment variable holding the Consul ACL token")
	fs.DurationVar(&o.timeout, "timeout", 5*time.Second, "fleet: per-node timeout")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage()) }
	cmd := "ls"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	if cmd == "fleet" {
		o.sub = "ls"
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			o.sub, args = args[0], args[1:]
		}
		if o.sub != "ls" && o.sub != "check" {
			return "", o, fmt.Errorf("fleet %q: want fleet ls or fleet check", o.sub)
		}
	}
	if err := fs.Parse(args); err != nil {
		return "", o, err
	}
	if fs.NArg() > 0 {
		return "", o, fmt.Errorf("unexpected argument %q: the command comes first, then its flags", fs.Arg(0))
	}
	switch o.exitOn {
	case "", "warn", "bad", "error":
	default:
		return "", o, fmt.Errorf("--exit-on %q: want warn, bad or error", o.exitOn)
	}
	if o.interval <= 0 {
		return "", o, fmt.Errorf("--interval %s: must be positive", o.interval)
	}
	if o.timeout <= 0 {
		return "", o, fmt.Errorf("--timeout %s: must be positive", o.timeout)
	}
	if cmd == "fleet" {
		if o.targets == "" && o.consul == "" {
			return "", o, fmt.Errorf("fleet needs --targets host:port,… or --consul (or CONSUL_HTTP_ADDR)")
		}
		if o.targets != "" {
			if _, err := fleet.Static(o.targets); err != nil {
				return "", o, err
			}
		}
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
  gpuledger fleet ls  every node's /ledger (--targets h:p,… or --consul): GPUs per node and job
  gpuledger fleet check  every node's findings, one policy, worst first (--json, --exit-on)
  gpuledger version

Flags: --nomad-addr --nomad-token-env --nomad-ca-cert --nomad-ca-path --nomad-client-cert
       --nomad-client-key --nomad-tls-server-name --docker --podman --nvidia-smi --proc --node --json
       --exit-on --encoder-max --temp-max --allow-unmanaged --no-idle --no-nomad --no-docker
       --listen --interval --history
       --targets --consul --consul-service --consul-token-env --timeout
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
		// Docker and, when present, Podman: both speak the Engine API, and a node may
		// run tasks under either driver.
		type engine struct {
			name   string
			client *containers.Client
		}
		engines := []engine{{"docker", containers.NewClient(o.docker)}}
		if ep := podmanEndpoint(o.podman, podmanSocket); ep != "" {
			engines = append(engines, engine{"podman", containers.NewClient(ep)})
		}
		for _, e := range engines {
			list, err := e.client.List(ctx)
			if err != nil {
				in.Errors = append(in.Errors, e.name+": "+err.Error())
			}
			for _, c := range list {
				in.Containers[c.ID] = c
			}
		}
		in.ContainerOf = func(pid int) string {
			id := containers.ContainerIDOf(o.proc, pid)
			if id == "" {
				return ""
			}
			if _, ok := in.Containers[id]; !ok {
				for _, e := range engines {
					if c, err := e.client.Inspect(ctx, id); err == nil {
						in.Containers[id] = c
						break
					}
				}
			}
			return id
		}
	}
	if !o.noNomad {
		nc, err := nomad.NewTLSClient(o.nomadAddr, o.tokenEnv, o.nomadTLS)
		nodeID := ""
		if err == nil {
			nodeID, err = nc.NodeID(ctx)
		}
		if err != nil {
			in.Errors = append(in.Errors, "nomad: "+err.Error())
		} else if nodeID == "" {
			in.Errors = append(in.Errors, "nomad: the agent at "+o.nomadAddr+" is not a client node")
		} else if res, allocs, err := nc.Reservations(ctx, nodeID); err != nil {
			in.Errors = append(in.Errors, "nomad: "+err.Error())
		} else {
			in.Reservations, in.Allocs = res, allocs
		}
	}
	return ledger.Build(in)
}

func policy(o options) findings.Policy {
	return findings.Policy{EncoderMax: o.encoderMax, TempMaxC: o.tempMax, AllowUnmanaged: o.allowUnmanaged, IdleIsFinding: !o.noIdle}
}

func main() {
	cmd, o, err := parse(os.Args[1:])
	if err == flag.ErrHelp {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gpuledger:", err)
		os.Exit(2)
	}
	ctx := context.Background()
	switch cmd {
	case "version":
		fmt.Println("gpuledger", version.Version)
	case "ls":
		l := annotate(collect(ctx, o), o)
		if o.jsonOut {
			json.NewEncoder(os.Stdout).Encode(l)
		} else {
			fmt.Println(render.Ledger(l))
			for _, e := range l.Errors {
				fmt.Fprintln(os.Stderr, "gpuledger:", e)
			}
		}
	case "check":
		l := annotate(collect(ctx, o), o)
		fs := findings.Evaluate(l, policy(o))
		if o.jsonOut {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"schema": ledger.Schema, "node": l.Node, "at": l.At, "findings": fs, "worst": findings.Worst(fs)})
		} else {
			fmt.Print(render.Findings(fs))
		}
		os.Exit(findings.ExitCode(fs, o.exitOn))
	case "serve":
		serve(ctx, o)
	case "fleet":
		os.Exit(runFleet(ctx, o))
	case "help", "-h", "--help":
		fmt.Print(usage())
	default:
		fmt.Fprintf(os.Stderr, "gpuledger: unknown command %q\n%s", cmd, usage())
		os.Exit(2)
	}
}

// maxGap is the longest silence between two reads still taken as continuous.
func maxGap(o options) time.Duration { return 3 * o.interval }

// annotate, for ls and check, reads the history serve writes and never writes it; an
// unreadable history is a source error like any other.
func annotate(l ledger.Ledger, o options) ledger.Ledger {
	if o.history == "" {
		return l
	}
	h, err := history.Load(o.history, maxGap(o))
	if err != nil {
		l.Errors = append(l.Errors, "history: "+err.Error())
		return l
	}
	h.Annotate(&l)
	return l
}

func serve(ctx context.Context, o options) {
	var mu sync.RWMutex
	h := history.New(maxGap(o))
	if o.history != "" {
		loaded, err := history.Load(o.history, maxGap(o))
		if err != nil {
			fmt.Fprintf(os.Stderr, "gpuledger: history %s unreadable, starting a new one: %v\n", o.history, err)
		} else {
			h = loaded
		}
	}
	// record observes, annotates and, with --history, saves: only serve writes it.
	record := func(l ledger.Ledger) ledger.Ledger {
		h.Observe(l)
		h.Annotate(&l)
		if o.history != "" {
			if err := h.Save(o.history); err != nil {
				fmt.Fprintln(os.Stderr, "gpuledger: history:", err)
			}
		}
		return l
	}
	current := record(collect(ctx, o))
	go func() {
		t := time.NewTicker(o.interval)
		defer t.Stop()
		for range t.C {
			l := record(collect(ctx, o))
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
	mux := newMux(snap, policy(o))
	fmt.Fprintf(os.Stderr, "gpuledger %s serving on %s (refresh %s)\n", version.Version, o.listen, o.interval)
	if err := http.ListenAndServe(o.listen, mux); err != nil {
		fmt.Fprintln(os.Stderr, "gpuledger:", err)
		os.Exit(1)
	}
}

// newMux serves the snapshot: /metrics, /ledger, /findings, and /healthz, which is 503
// with the failing sources named while any source is down.
func newMux(snap func() ledger.Ledger, p findings.Policy) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		l := snap()
		fmt.Fprint(w, metrics.Render(l, findings.Evaluate(l, p), findings.Codes(p)))
	})
	mux.HandleFunc("/ledger", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap())
	})
	mux.HandleFunc("/findings", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		l := snap()
		fs := findings.Evaluate(l, p)
		json.NewEncoder(w).Encode(map[string]any{"schema": ledger.Schema, "node": l.Node, "at": l.At, "findings": fs, "worst": findings.Worst(fs)})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if errs := snap().Errors; len(errs) > 0 {
			w.WriteHeader(503)
			fmt.Fprintln(w, strings.Join(errs, "\n"))
			return
		}
		fmt.Fprintln(w, "ok")
	})
	return mux
}

// discover returns the endpoints: --targets as given, else Consul's passing instances.
// A discovery that fails, or finds nothing, is reported as a node that could not be
// read, so it lands in the table and the findings like any other unreadable source.
func discover(ctx context.Context, o options) ([]fleet.Target, []fleet.Node) {
	if o.targets != "" {
		ts, _ := fleet.Static(o.targets) // validated by parse
		return ts, nil
	}
	ts, err := fleet.NewConsul(o.consul, o.consulTokenEnv).Targets(ctx, o.consulService)
	if err == nil && len(ts) == 0 {
		err = fmt.Errorf("Consul has no passing instance of service %q", o.consulService)
	}
	if err != nil {
		return nil, []fleet.Node{{Target: fleet.Target{Node: "consul", URL: o.consul}, Err: err.Error()}}
	}
	return ts, nil
}

func runFleet(ctx context.Context, o options) int {
	targets, failed := discover(ctx, o)
	nodes := append(failed, fleet.Fetch(ctx, targets, o.timeout)...)
	at := time.Now()
	switch o.sub {
	case "check":
		fs := fleet.Findings(nodes, policy(o))
		if o.jsonOut {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"schema": ledger.Schema, "at": at, "findings": fs, "worst": findings.Worst(fs)})
		} else {
			fmt.Print(render.FleetFindings(fs))
		}
		return findings.ExitCode(fs, o.exitOn)
	default:
		s := fleet.Summarise(nodes)
		if o.jsonOut {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"schema": ledger.Schema, "at": at, "summary": s})
		} else {
			fmt.Println(render.Fleet(s))
		}
		return 0
	}
}

// podmanSocket is where a rootful Podman serves its API (podman.socket).
const podmanSocket = "/run/podman/podman.sock"

// podmanEndpoint resolves --podman: "auto" is the socket when it exists and nothing
// otherwise, so a node without Podman has no Podman error; "off" is nothing; anything
// else is used as given, and a failure to read it is a source error.
func podmanEndpoint(flag, socket string) string {
	switch flag {
	case "off", "":
		return ""
	case "auto":
		if _, err := os.Stat(socket); err == nil {
			return "unix://" + socket
		}
		return ""
	}
	return flag
}
