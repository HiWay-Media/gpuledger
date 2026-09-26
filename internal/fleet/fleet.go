// Package fleet reads every node's /ledger and answers for the cluster: GPUs per node
// and per job, held, reserved-idle, unaccounted, free. It asks gpuledger's own
// endpoint, never the nodes' Nomad, Docker or driver, so a node is read with exactly the
// view its agent has; the findings are evaluated here, with one policy for the fleet.
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nomad"
)

// Target is one gpuledger serve endpoint. Node is the name to report it under until
// its ledger names itself.
type Target struct {
	Node string `json:"node"`
	URL  string `json:"url"`
}

// Static parses "host:port,http://host:port,…". A port is required: gpuledger has no
// default port a scheme would imply.
func Static(list string) ([]Target, error) {
	var out []Target
	for _, raw := range strings.Split(list, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Port() == "" {
			return nil, fmt.Errorf("target %q: want host:port or http(s)://host:port", raw)
		}
		out = append(out, Target{Node: u.Hostname(), URL: u.Scheme + "://" + u.Host})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no targets")
	}
	return out, nil
}

// Consul discovers the gpuledger instances Consul has as passing.
type Consul struct {
	http  *http.Client
	addr  string
	token string
}

// NewConsul takes CONSUL_HTTP_ADDR's forms (with or without a scheme) and reads the ACL
// token from the environment variable named by tokenEnv, never from a flag value.
func NewConsul(addr, tokenEnv string) *Consul {
	if addr == "" {
		addr = "127.0.0.1:8500"
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	tok := ""
	if tokenEnv != "" {
		tok = os.Getenv(tokenEnv)
	}
	return &Consul{http: &http.Client{Timeout: 5 * time.Second}, addr: strings.TrimRight(addr, "/"), token: tok}
}

// Targets asks /v1/health/service/<service>?passing=true; the service address wins
// over the node's, as Consul's own DNS answers it.
func (c *Consul) Targets(ctx context.Context, service string) ([]Target, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.addr+"/v1/health/service/"+url.PathEscape(service)+"?passing=true", nil)
	if c.token != "" {
		req.Header.Set("X-Consul-Token", c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("consul: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("consul /v1/health/service/%s: HTTP %d", service, res.StatusCode)
	}
	var entries []struct {
		Node struct {
			Node    string
			Address string
		}
		Service struct {
			Address string
			Port    int
		}
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("consul: %w", err)
	}
	var out []Target
	for _, e := range entries {
		host := e.Service.Address
		if host == "" {
			host = e.Node.Address
		}
		out = append(out, Target{Node: e.Node.Node, URL: "http://" + net.JoinHostPort(host, fmt.Sprint(e.Service.Port))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out, nil
}

// FromNomad turns Nomad service registrations into targets, sorted. A registration
// names a node by id only; its ledger names it once read.
func FromNomad(svcs []nomad.Service) []Target {
	var out []Target
	for _, sv := range svcs {
		out = append(out, Target{Node: sv.Address, URL: "http://" + net.JoinHostPort(sv.Address, fmt.Sprint(sv.Port))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

// Node is one target read: its ledger, or why it could not be read.
type Node struct {
	Target Target        `json:"target"`
	Ledger ledger.Ledger `json:"ledger"`
	Err    string        `json:"error,omitempty"`
}

// Name is the node's own name when its ledger was read, the target's otherwise.
func (n Node) Name() string {
	if n.Err == "" && n.Ledger.Node != "" {
		return n.Ledger.Node
	}
	return n.Target.Node
}

// Fetch reads every target's /ledger at once, each bounded by timeout, and returns
// them sorted by name. A node that cannot be read is a Node with Err, never a failure
// of the whole fleet.
func Fetch(ctx context.Context, targets []Target, timeout time.Duration) []Node {
	out := make([]Node, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = fetch(ctx, t, timeout)
		}()
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func fetch(ctx context.Context, t Target, timeout time.Duration) Node {
	n := Node{Target: t}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", t.URL+"/ledger", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is the target's; keep what went wrong
		}
		n.Err = err.Error()
		return n
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		n.Err = fmt.Sprintf("%s/ledger: HTTP %d", t.URL, res.StatusCode)
		return n
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&n.Ledger); err != nil {
		n.Err = fmt.Sprintf("%s/ledger: %v", t.URL, err)
	}
	return n
}

// NodeSummary counts one node's GPUs by state. Every GPU is in exactly one of Held,
// ReservedIdle, Unaccounted and Free.
type NodeSummary struct {
	Node         string `json:"node"`
	Schema       int    `json:"schema"` // the node's ledger schema; 0 is a gpuledger before 0.3
	GPUs         int    `json:"gpus"`
	Held         int    `json:"held"`         // tenants, every one the reservation's
	ReservedIdle int    `json:"reservedIdle"` // reserved, nothing on it
	Unaccounted  int    `json:"unaccounted"`  // a tenant Nomad did not reserve the card for, or cannot vouch for
	Free         int    `json:"free"`         // no tenant, no reservation
	// Memory in MiB across the node's GPUs.
	MemoryUsedMiB  int    `json:"memoryUsedMiB"`
	MemoryTotalMiB int    `json:"memoryTotalMiB"`
	Unreachable    int    `json:"unreachable,omitempty"` // in the fleet total: nodes not read
	Err            string `json:"error,omitempty"`
}

// JobSummary is one Nomad job's GPUs across the fleet: how many it was reserved and
// how many of those it actually holds.
type JobSummary struct {
	Namespace string `json:"namespace"`
	Job       string `json:"job"`
	Reserved  int    `json:"reserved"`
	Held      int    `json:"held"`
}

// Summary is the fleet: per node, per job, and the total.
type Summary struct {
	Schema int           `json:"schema"`
	Nodes  []NodeSummary `json:"nodes"`
	Jobs   []JobSummary  `json:"jobs"`
	Total  NodeSummary   `json:"total"`
}

// Summarise counts; it is pure, the nodes are what Fetch read.
func Summarise(nodes []Node) Summary {
	s := Summary{Schema: ledger.Schema, Nodes: []NodeSummary{}, Jobs: []JobSummary{}, Total: NodeSummary{Node: "fleet", Schema: ledger.Schema}}
	jobs := map[[2]string]*JobSummary{}
	for _, n := range nodes {
		ns := NodeSummary{Node: n.Name(), Schema: n.Ledger.Schema, Err: n.Err}
		if n.Err != "" {
			s.Total.Unreachable++
			s.Nodes = append(s.Nodes, ns)
			continue
		}
		for _, e := range n.Ledger.Entries {
			ns.GPUs++
			ns.MemoryUsedMiB += e.MemoryUsedMiB
			ns.MemoryTotalMiB += e.MemoryTotalMiB
			// Classified here, not read from the node: an older gpuledger sends no state.
			switch ledger.Classify(e) {
			case ledger.StateFree:
				ns.Free++
			case ledger.StateReservedIdle:
				ns.ReservedIdle++
			case ledger.StateHeld:
				ns.Held++
			default:
				ns.Unaccounted++
			}
			seen := map[[2]string]bool{}
			for _, r := range e.Reservations {
				ns := r.Namespace
				if ns == "" {
					ns = "default"
				}
				k := [2]string{ns, r.JobID}
				if seen[k] {
					continue
				}
				seen[k] = true
				j := jobs[k]
				if j == nil {
					j = &JobSummary{Namespace: ns, Job: r.JobID}
					jobs[k] = j
				}
				j.Reserved++
				if holds(e.Tenants, r.AllocID) {
					j.Held++
				}
			}
		}
		s.Nodes = append(s.Nodes, ns)
		s.Total.GPUs += ns.GPUs
		s.Total.Held += ns.Held
		s.Total.ReservedIdle += ns.ReservedIdle
		s.Total.Unaccounted += ns.Unaccounted
		s.Total.Free += ns.Free
		s.Total.MemoryUsedMiB += ns.MemoryUsedMiB
		s.Total.MemoryTotalMiB += ns.MemoryTotalMiB
	}
	for _, j := range jobs {
		s.Jobs = append(s.Jobs, *j)
	}
	sort.Slice(s.Jobs, func(i, j int) bool {
		if s.Jobs[i].Namespace != s.Jobs[j].Namespace {
			return s.Jobs[i].Namespace < s.Jobs[j].Namespace
		}
		return s.Jobs[i].Job < s.Jobs[j].Job
	})
	return s
}

func holds(ts []ledger.Tenant, alloc string) bool {
	for _, t := range ts {
		if t.AllocID == alloc && t.Reserved {
			return true
		}
	}
	return false
}

// Findings evaluates every node's ledger with one policy; a node that could not be
// read is a source-unavailable ERROR. Worst first across the fleet.
func Findings(nodes []Node, p findings.Policy) []findings.Finding {
	var out []findings.Finding
	for _, n := range nodes {
		if n.Err != "" {
			out = append(out, findings.Finding{Level: findings.ERROR, Code: "source-unavailable", Node: n.Name(), Message: fmt.Sprintf("gpuledger at %s could not be read: %s", n.Target.URL, n.Err)})
			continue
		}
		out = append(out, findings.Evaluate(n.Ledger, p)...)
	}
	findings.Sort(out)
	return out
}
