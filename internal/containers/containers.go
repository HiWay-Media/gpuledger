// Package containers answers "which container owns this PID, and who owns the
// container": the cgroup of the process names a container id, the Docker API names the
// container, and Nomad's labels on it name the allocation, job and task.
package containers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Container is what the ledger keeps about one container: names and labels, never env.
type Container struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Labels    map[string]string `json:"labels,omitempty"`
	GPUs      []string          `json:"gpus,omitempty"` // NVIDIA_VISIBLE_DEVICES, when set
	AllocID   string            `json:"allocId,omitempty"`
	JobName   string            `json:"jobName,omitempty"`
	TaskName  string            `json:"taskName,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
}

// NomadManaged is true when Nomad's docker driver started the container.
func (c Container) NomadManaged() bool { return c.AllocID != "" }

var idRe = regexp.MustCompile(`([0-9a-f]{64})`)

// ContainerIDOf reads /proc/<pid>/cgroup under root and returns the 64-hex container id
// it names, or "" for a host process. Both cgroup v1 and v2 layouts put the id in the
// path (docker/<id>, docker-<id>.scope, containerd-<id>.scope).
func ContainerIDOf(procRoot string, pid int) string {
	b, err := os.ReadFile(filepath.Join(procRoot, fmt.Sprint(pid), "cgroup"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if m := idRe.FindStringSubmatch(line); m != nil && (strings.Contains(line, "docker") || strings.Contains(line, "containerd") || strings.Contains(line, "nomad")) {
			return m[1]
		}
	}
	return ""
}

// Client talks to the Docker Engine API over a unix socket or an http base URL.
type Client struct {
	http *http.Client
	base string
}

// NewClient accepts "unix:///var/run/docker.sock" (the default) or "http://host:port".
func NewClient(endpoint string) *Client {
	if endpoint == "" {
		endpoint = "unix:///var/run/docker.sock"
	}
	if strings.HasPrefix(endpoint, "unix://") {
		path := strings.TrimPrefix(endpoint, "unix://")
		tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}}
		return &Client{http: &http.Client{Transport: tr, Timeout: 5 * time.Second}, base: "http://docker"}
	}
	return &Client{http: &http.Client{Timeout: 5 * time.Second}, base: strings.TrimRight(endpoint, "/")}
}

type inspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	HostConfig struct {
		DeviceRequests []struct {
			Driver       string   `json:"Driver"`
			Count        int      `json:"Count"`
			DeviceIDs    []string `json:"DeviceIDs"`
			Capabilities [][]string
		} `json:"DeviceRequests"`
		Runtime string `json:"Runtime"`
	} `json:"HostConfig"`
}

func fromInspect(in inspect) Container {
	c := Container{ID: in.ID, Name: strings.TrimPrefix(in.Name, "/"), Image: in.Config.Image, Labels: map[string]string{}}
	for k, v := range in.Config.Labels {
		if strings.HasPrefix(k, "com.hashicorp.nomad.") {
			c.Labels[k] = v
		}
	}
	c.AllocID = in.Config.Labels["com.hashicorp.nomad.allocation_id"]
	if c.AllocID == "" {
		c.AllocID = in.Config.Labels["com.hashicorp.nomad.alloc_id"]
	}
	c.JobName = in.Config.Labels["com.hashicorp.nomad.job_name"]
	c.TaskName = in.Config.Labels["com.hashicorp.nomad.task_name"]
	c.Namespace = in.Config.Labels["com.hashicorp.nomad.namespace"]
	// Only the one environment variable that names GPUs is read; the rest of Env is
	// never looked at — it is where secrets live.
	for _, e := range in.Config.Env {
		if strings.HasPrefix(e, "NVIDIA_VISIBLE_DEVICES=") {
			v := strings.TrimPrefix(e, "NVIDIA_VISIBLE_DEVICES=")
			if v != "" && v != "void" && v != "none" {
				c.GPUs = strings.Split(v, ",")
			}
		}
	}
	for _, d := range in.HostConfig.DeviceRequests {
		if len(d.DeviceIDs) > 0 && len(c.GPUs) == 0 {
			c.GPUs = append(c.GPUs, d.DeviceIDs...)
		} else if d.Count != 0 && len(c.GPUs) == 0 {
			c.GPUs = []string{"all"}
		}
	}
	return c
}

// Inspect returns one container by id.
func (c *Client) Inspect(ctx context.Context, id string) (Container, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/containers/"+id+"/json", nil)
	res, err := c.http.Do(req)
	if err != nil {
		return Container{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Container{}, fmt.Errorf("docker inspect %s: HTTP %d", short(id), res.StatusCode)
	}
	var in inspect
	if err := json.NewDecoder(res.Body).Decode(&in); err != nil {
		return Container{}, err
	}
	return fromInspect(in), nil
}

// List returns every running container, inspected — so a GPU tenant with no process
// at this instant (a worker between jobs) is still known to hold a device.
func (c *Client) List(ctx context.Context) ([]Container, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/containers/json", nil)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("docker list: HTTP %d", res.StatusCode)
	}
	var brief []struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&brief); err != nil {
		return nil, err
	}
	var out []Container
	for _, b := range brief {
		ct, err := c.Inspect(ctx, b.ID)
		if err != nil {
			continue
		}
		out = append(out, ct)
	}
	return out, nil
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
