// Package nomad asks the local agent which allocations on this node were given which
// GPU device ids — the reservation side of the ledger.
package nomad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Reservation is one task's claim on GPU device ids, as Nomad scheduled it.
type Reservation struct {
	AllocID   string   `json:"allocId"`
	JobID     string   `json:"jobId"`
	TaskGroup string   `json:"taskGroup"`
	Task      string   `json:"task"`
	Namespace string   `json:"namespace"`
	Status    string   `json:"clientStatus"`
	DeviceIDs []string `json:"deviceIds"`
}

// Client is a minimal Nomad HTTP client: agent self, node allocations.
type Client struct {
	http  *http.Client
	addr  string
	token string
}

// NewClient reads the ACL token from the environment variable named by tokenEnv
// (never from a flag or a file), so the token is neither on the command line nor logged.
func NewClient(addr, tokenEnv string) *Client {
	if addr == "" {
		addr = "http://127.0.0.1:4646"
	}
	tok := ""
	if tokenEnv != "" {
		tok = os.Getenv(tokenEnv)
	}
	return &Client{http: &http.Client{Timeout: 5 * time.Second}, addr: strings.TrimRight(addr, "/"), token: tok}
}

// get decodes one GET. need names the ACL capability the path requires, so a 403 says
// what the token lacks.
func (c *Client) get(ctx context.Context, path, need string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.addr+path, nil)
	if c.token != "" {
		req.Header.Set("X-Nomad-Token", c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 403 {
		return fmt.Errorf("nomad %s: HTTP 403 — the token in the variable named by --nomad-token-env needs %s", path, need)
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("nomad %s: HTTP %d", path, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// NodeID returns this agent's client node id, or "" when the agent is not a client.
func (c *Client) NodeID(ctx context.Context) (string, error) {
	var self struct {
		Stats struct {
			Client map[string]string `json:"client"`
		} `json:"stats"`
	}
	if err := c.get(ctx, "/v1/agent/self", "agent:read", &self); err != nil {
		return "", err
	}
	return self.Stats.Client["node_id"], nil
}

type allocation struct {
	ID                 string `json:"ID"`
	JobID              string `json:"JobID"`
	Namespace          string `json:"Namespace"`
	TaskGroup          string `json:"TaskGroup"`
	ClientStatus       string `json:"ClientStatus"`
	AllocatedResources struct {
		Tasks map[string]struct {
			Devices []struct {
				Vendor    string   `json:"Vendor"`
				Type      string   `json:"Type"`
				Name      string   `json:"Name"`
				DeviceIDs []string `json:"DeviceIDs"`
			} `json:"Devices"`
		} `json:"Tasks"`
	} `json:"AllocatedResources"`
}

// Reservations lists, for the node, every running or pending allocation task that holds
// NVIDIA GPUs (the device plugin's vendor "nvidia", type "gpu"). Other devices and
// allocations without devices are skipped: the join could never match them.
//
// allocs is every allocation returned, id → namespace, whatever its status: Nomad
// leaves out, without an error, the allocations in namespaces the token cannot read-job.
func (c *Client) Reservations(ctx context.Context, nodeID string) (out []Reservation, allocs map[string]string, err error) {
	var list []allocation
	if err := c.get(ctx, "/v1/node/"+nodeID+"/allocations", "node:read", &list); err != nil {
		return nil, nil, err
	}
	allocs = map[string]string{}
	for _, a := range list {
		ns := a.Namespace
		if ns == "" {
			ns = "default"
		}
		allocs[a.ID] = ns
	}
	for _, a := range list {
		if a.ClientStatus != "running" && a.ClientStatus != "pending" {
			continue
		}
		for task, t := range a.AllocatedResources.Tasks {
			for _, d := range t.Devices {
				if d.Type != "gpu" || !strings.EqualFold(d.Vendor, "nvidia") {
					continue
				}
				out = append(out, Reservation{AllocID: a.ID, JobID: a.JobID, TaskGroup: a.TaskGroup, Task: task, Namespace: a.Namespace, Status: a.ClientStatus, DeviceIDs: d.DeviceIDs})
			}
		}
	}
	return out, allocs, nil
}
