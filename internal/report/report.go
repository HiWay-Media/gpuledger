// Package report reads gpuledger's counters back from Prometheus for a window and says
// what the GPUs cost: per job, GPU-hours reserved, held and left idle; per node, hours
// in each state. It asks Prometheus only — the history is Prometheus's, not a file's.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hiway-media/gpuledger/internal/ledger"
)

// Job is one job's GPU-hours over the window. IdleShare is Idle over Reserved, 0 when
// nothing was reserved.
type Job struct {
	Namespace     string  `json:"namespace"`
	Job           string  `json:"job"`
	ReservedHours float64 `json:"reservedHours"`
	HeldHours     float64 `json:"heldHours"`
	IdleHours     float64 `json:"idleHours"`
	IdleShare     float64 `json:"idleShare"`
}

// Node is one node's GPU-hours in each state over the window.
type Node struct {
	Node  string             `json:"node"`
	Hours map[string]float64 `json:"hours"`
}

// Report is the answer, under the JSON contract.
type Report struct {
	Schema int       `json:"schema"`
	Since  string    `json:"since"`
	At     time.Time `json:"at"`
	Jobs   []Job     `json:"jobs"`
	Nodes  []Node    `json:"nodes"`
}

// Client asks one Prometheus.
type Client struct {
	http  *http.Client
	base  string
	token string
}

// New takes the Prometheus base URL and the name of the variable holding a bearer
// token, if any — never the token itself.
func New(base, tokenEnv string) *Client {
	tok := ""
	if tokenEnv != "" {
		tok = os.Getenv(tokenEnv)
	}
	return &Client{http: &http.Client{Timeout: 30 * time.Second}, base: strings.TrimRight(base, "/"), token: tok}
}

// window is a Prometheus range, e.g. 7d or 90m: it goes into the query, so nothing else
// is let through.
var window = regexp.MustCompile(`^[1-9][0-9]*(ms|s|m|h|d|w|y)$`)

type sample struct {
	Metric map[string]string
	Value  float64
}

func (c *Client) query(ctx context.Context, q string) ([]sample, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/api/v1/query?query="+url.QueryEscape(q), nil)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus: %w", err)
	}
	defer res.Body.Close()
	var r struct {
		Status, Error string
		Data          struct {
			Result []struct {
				Metric map[string]string
				Value  [2]any
			}
		}
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("prometheus: HTTP %d: %w", res.StatusCode, err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", r.Error)
	}
	var out []sample
	for _, s := range r.Data.Result {
		str, _ := s.Value[1].(string)
		v, err := strconv.ParseFloat(str, 64)
		if err != nil {
			continue
		}
		out = append(out, sample{Metric: s.Metric, Value: v})
	}
	return out, nil
}

// Build runs the two queries for the window since.
func (c *Client) Build(ctx context.Context, since string) (Report, error) {
	r := Report{Schema: ledger.Schema, Since: since, At: time.Now(), Jobs: []Job{}, Nodes: []Node{}}
	if !window.MatchString(since) {
		return r, fmt.Errorf("--since %q: want a Prometheus duration such as 7d, 24h or 90m", since)
	}
	jobs, err := c.query(ctx, fmt.Sprintf("sum by (namespace, nomad_job, use) (increase(gpuledger_job_gpu_seconds_total[%s]))", since))
	if err != nil {
		return r, err
	}
	byJob := map[[2]string]*Job{}
	for _, s := range jobs {
		k := [2]string{s.Metric["namespace"], s.Metric["nomad_job"]}
		j := byJob[k]
		if j == nil {
			j = &Job{Namespace: k[0], Job: k[1]}
			byJob[k] = j
		}
		switch s.Metric["use"] {
		case "held":
			j.HeldHours += s.Value / 3600
		case "idle":
			j.IdleHours += s.Value / 3600
		}
	}
	for _, j := range byJob {
		j.ReservedHours = j.HeldHours + j.IdleHours
		if j.ReservedHours > 0 {
			j.IdleShare = j.IdleHours / j.ReservedHours
		}
		r.Jobs = append(r.Jobs, *j)
	}
	// The most idle GPU-hours first: the waste worth a look.
	sort.Slice(r.Jobs, func(a, b int) bool {
		if r.Jobs[a].IdleHours != r.Jobs[b].IdleHours {
			return r.Jobs[a].IdleHours > r.Jobs[b].IdleHours
		}
		return r.Jobs[a].Namespace+"/"+r.Jobs[a].Job < r.Jobs[b].Namespace+"/"+r.Jobs[b].Job
	})
	states, err := c.query(ctx, fmt.Sprintf("sum by (node, state) (increase(gpuledger_gpu_state_seconds_total[%s]))", since))
	if err != nil {
		return r, err
	}
	byNode := map[string]*Node{}
	for _, s := range states {
		n := byNode[s.Metric["node"]]
		if n == nil {
			n = &Node{Node: s.Metric["node"], Hours: map[string]float64{}}
			byNode[s.Metric["node"]] = n
		}
		n.Hours[s.Metric["state"]] += s.Value / 3600
	}
	for _, n := range byNode {
		r.Nodes = append(r.Nodes, *n)
	}
	sort.Slice(r.Nodes, func(a, b int) bool { return r.Nodes[a].Node < r.Nodes[b].Node })
	return r, nil
}
