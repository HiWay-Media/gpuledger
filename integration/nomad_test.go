//go:build integration

// Package integration runs gpuledger against a real Nomad agent in -dev mode, one
// version at a time (CI's nomad matrix). No GPU is needed: Nomad's example device
// plugin, rebuilt with vendor "nvidia" and type "gpu", exposes files named like GPU
// UUIDs as devices, so the scheduler, the allocation API and the docker driver's labels
// are Nomad's own, and only nvidia-smi is a fake — one that names the real pids.
//
//	go test -tags integration -c -o it.test ./integration
//	sudo NOMAD_BIN=/path/to/nomad GPULEDGER_DEVICE_PLUGIN=/path/to/nomad-device-fake \
//	  GPULEDGER_BIN=/path/to/gpuledger ./it.test -test.v
//
// Linux, root (the Nomad client), Docker with busybox pulled. PROMTOOL, when set,
// validates /metrics as well.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const image = "busybox:1.36"

var gpus = []string{
	"GPU-00000000-1111-4000-8000-000000000000",
	"GPU-11111111-1111-4000-8000-000000000001",
	"GPU-22222222-1111-4000-8000-000000000002",
}

type agent struct {
	t    *testing.T
	addr string
	mgmt string // management token, for setup only; gpuledger never gets it
}

func (a *agent) do(method, path, token string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.addr+path, rd)
	if token != "" {
		req.Header.Set("X-Nomad-Token", token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return res.StatusCode, fmt.Errorf("%s %s: HTTP %d: %s", method, path, res.StatusCode, b)
	}
	if out != nil {
		return 200, json.Unmarshal(b, out)
	}
	return 200, nil
}

func (a *agent) must(method, path string, body, out any) {
	a.t.Helper()
	if _, err := a.do(method, path, a.mgmt, body, out); err != nil {
		a.t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, d time.Duration, f func() (bool, string)) {
	t.Helper()
	last := ""
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(500 * time.Millisecond) {
		ok, why := f()
		if ok {
			return
		}
		last = why
	}
	t.Fatalf("timed out waiting for %s: %s", what, last)
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// start runs the agent with ACLs on, the fake GPU plugin and the docker driver's
// extra_labels set the way the README tells operators to set them.
func start(t *testing.T) *agent {
	nomadBin, plugin := os.Getenv("NOMAD_BIN"), os.Getenv("GPULEDGER_DEVICE_PLUGIN")
	if nomadBin == "" || plugin == "" {
		t.Skip("NOMAD_BIN and GPULEDGER_DEVICE_PLUGIN are required")
	}
	dir := t.TempDir()
	devices, plugins := filepath.Join(dir, "devices"), filepath.Join(dir, "plugins")
	os.MkdirAll(devices, 0o755)
	os.MkdirAll(plugins, 0o755)
	for _, g := range gpus {
		os.WriteFile(filepath.Join(devices, g), nil, 0o644)
	}
	b, err := os.ReadFile(plugin)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(plugins, "nomad-device-fake"), b, 0o755)
	port := freePort(t)
	cfg := fmt.Sprintf(`
data_dir   = %q
plugin_dir = %q
ports { http = %d  rpc = %d  serf = %d }
acl { enabled = true }
plugin "nomad-device-fake" {
  config {
    dir         = %q
    list_period = "1s"
  }
}
plugin "docker" {
  config {
    extra_labels = ["job_name", "task_group_name", "task_name", "namespace", "node_name"]
  }
}
`, filepath.Join(dir, "data"), plugins, port, freePort(t), freePort(t), devices)
	os.WriteFile(filepath.Join(dir, "agent.hcl"), []byte(cfg), 0o644)
	log, _ := os.Create(filepath.Join(dir, "agent.log"))
	cmd := exec.Command(nomadBin, "agent", "-dev", "-config", filepath.Join(dir, "agent.hcl"))
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			cmd.Process.Kill()
		}
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(dir, "agent.log"))
			if len(b) > 20000 {
				b = b[len(b)-20000:]
			}
			t.Logf("agent log (tail):\n%s", b)
		}
	})
	a := &agent{t: t, addr: fmt.Sprintf("http://127.0.0.1:%d", port)}
	var boot struct{ SecretID string }
	eventually(t, "ACL bootstrap", 60*time.Second, func() (bool, string) {
		_, err := a.do("POST", "/v1/acl/bootstrap", "", nil, &boot)
		return err == nil && boot.SecretID != "", fmt.Sprint(err)
	})
	a.mgmt = boot.SecretID
	eventually(t, "three healthy nvidia/gpu devices on the node", 60*time.Second, func() (bool, string) {
		var nodes []struct{ ID, Status string }
		if _, err := a.do("GET", "/v1/nodes", a.mgmt, nil, &nodes); err != nil || len(nodes) == 0 || nodes[0].Status != "ready" {
			return false, fmt.Sprint(err, nodes)
		}
		var node struct {
			NodeResources struct {
				Devices []struct {
					Vendor, Type string
					Instances    []struct {
						ID      string
						Healthy bool
					}
				}
			}
		}
		if _, err := a.do("GET", "/v1/node/"+nodes[0].ID, a.mgmt, nil, &node); err != nil {
			return false, err.Error()
		}
		for _, d := range node.NodeResources.Devices {
			healthy := 0
			for _, i := range d.Instances {
				if i.Healthy {
					healthy++
				}
			}
			if d.Vendor == "nvidia" && d.Type == "gpu" && healthy == len(gpus) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("%+v", node.NodeResources.Devices)
	})
	return a
}

// token creates a client token with exactly these rules and returns its secret.
func (a *agent) token(name, rules string) string {
	a.must("POST", "/v1/acl/policy/"+name, map[string]any{"Name": name, "Rules": rules}, nil)
	var tok struct{ SecretID string }
	a.must("POST", "/v1/acl/token", map[string]any{"Name": name, "Type": "client", "Policies": []string{name}}, &tok)
	return tok.SecretID
}

func gpuJob(id, namespace string) map[string]any {
	return map[string]any{"Job": map[string]any{
		"ID": id, "Name": id, "Namespace": namespace, "Type": "service", "Datacenters": []string{"dc1"},
		"TaskGroups": []any{map[string]any{
			"Name": "g", "Count": 1,
			"RestartPolicy": map[string]any{"Attempts": 0, "Mode": "fail"},
			"Tasks": []any{map[string]any{
				"Name": id, "Driver": "docker",
				"Config":    map[string]any{"image": image, "command": "sleep", "args": []string{"3600"}},
				"Resources": map[string]any{"CPU": 50, "MemoryMB": 32, "Devices": []any{map[string]any{"Name": "nvidia/gpu", "Count": 1}}},
			}},
		}},
	}}
}

type alloc struct {
	ID, ClientStatus   string
	AllocatedResources struct {
		Tasks map[string]struct {
			Devices []struct{ DeviceIDs []string }
		}
	}
}

// running waits for the job's allocation and returns it with the GPU it was given.
func (a *agent) running(job, namespace string) (alloc, string) {
	var got alloc
	eventually(a.t, job+" running", 120*time.Second, func() (bool, string) {
		var list []struct{ ID, ClientStatus string }
		if _, err := a.do("GET", "/v1/job/"+job+"/allocations?namespace="+namespace, a.mgmt, nil, &list); err != nil || len(list) == 0 {
			return false, fmt.Sprint(err)
		}
		if list[0].ClientStatus != "running" {
			return false, list[0].ClientStatus
		}
		_, err := a.do("GET", "/v1/allocation/"+list[0].ID+"?namespace="+namespace, a.mgmt, nil, &got)
		return err == nil, fmt.Sprint(err)
	})
	for _, t := range got.AllocatedResources.Tasks {
		for _, d := range t.Devices {
			if len(d.DeviceIDs) == 1 {
				return got, d.DeviceIDs[0]
			}
		}
	}
	a.t.Fatalf("%s: no single device id in %+v", job, got.AllocatedResources)
	return got, ""
}

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAgainstARealNomad(t *testing.T) {
	a := start(t)
	a.must("POST", "/v1/namespace/video", map[string]any{"Name": "video"}, nil)

	// The token gpuledger gets: the least the README says it needs.
	minimal := a.token("gpuledger", `agent { policy = "read" }
node { policy = "read" }`)
	agentOnly := a.token("agent-only", `agent { policy = "read" }`)

	a.must("POST", "/v1/jobs", gpuJob("enc", "default"), nil)
	a.must("POST", "/v1/jobs?namespace=video", gpuJob("idle", "video"), nil)
	t.Cleanup(func() {
		a.do("DELETE", "/v1/job/enc?purge=true", a.mgmt, nil, nil)
		a.do("DELETE", "/v1/job/idle?namespace=video&purge=true", a.mgmt, nil, nil)
		time.Sleep(3 * time.Second)
	})
	enc, encGPU := a.running("enc", "default")
	idle, idleGPU := a.running("idle", "video")
	free := ""
	for _, g := range gpus {
		if g != encGPU && g != idleGPU {
			free = g
		}
	}
	if encGPU == idleGPU || free == "" {
		t.Fatalf("two jobs, one GPU each: enc=%s idle=%s", encGPU, idleGPU)
	}

	// What the docker driver really put on the container: logged, so the facts in
	// CLAUDE.md can be checked against every version in the matrix.
	cid := sh(t, "docker", "ps", "-q", "--no-trunc", "--filter", "label=com.hashicorp.nomad.alloc_id="+enc.ID)
	if cid == "" {
		cid = sh(t, "docker", "ps", "-q", "--no-trunc", "--filter", "label=com.hashicorp.nomad.allocation_id="+enc.ID)
	}
	if cid == "" {
		t.Fatalf("no container carries the alloc id label for %s:\n%s", enc.ID, sh(t, "docker", "ps", "--no-trunc", "--format", "{{.ID}} {{.Labels}}"))
	}
	t.Logf("docker labels of the enc task: %s", sh(t, "docker", "inspect", "-f", "{{json .Config.Labels}}", cid))
	pid := sh(t, "docker", "inspect", "-f", "{{.State.Pid}}", cid)
	cg, _ := os.ReadFile("/proc/" + pid + "/cgroup")
	t.Logf("cgroup of the enc task (pid %s): %s", pid, bytes.TrimSpace(cg))

	// A container nobody orchestrates, holding the free GPU by environment only.
	rogue := sh(t, "docker", "run", "-d", "--rm", "--name", "gpuledger-it-rogue", "-e", "NVIDIA_VISIBLE_DEVICES="+free, image, "sleep", "600")
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", rogue).Run() })

	// nvidia-smi as the driver would answer: enc's process on its GPU and, the case
	// Nomad cannot see, on the free one too.
	dir := t.TempDir()
	var qg, qa strings.Builder
	for i, g := range gpus {
		fmt.Fprintf(&qg, "%d, %s, Fake RTX, 00000000:0%d:00.0, 100, 8192, 5, 1, 40, 30.5, 0, 0\n", i, g, i+1)
	}
	fmt.Fprintf(&qa, "%s, %s, 512, /usr/bin/ffmpeg -i /data/secret/in.ts\n%s, %s, 64, ffmpeg\n", encGPU, pid, free, pid)
	os.WriteFile(filepath.Join(dir, "gpu.csv"), []byte(qg.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "apps.csv"), []byte(qa.String()), 0o644)
	smi := filepath.Join(dir, "nvidia-smi")
	os.WriteFile(smi, []byte(fmt.Sprintf("#!/bin/sh\ncase \"$1\" in --query-gpu=*) cat %q ;; *) cat %q ;; esac\n", filepath.Join(dir, "gpu.csv"), filepath.Join(dir, "apps.csv"))), 0o755)

	bin := os.Getenv("GPULEDGER_BIN")
	if bin == "" {
		bin = filepath.Join(dir, "gpuledger")
		sh(t, "go", "build", "-o", bin, "../cmd/gpuledger")
	}
	gl := func(token string, args ...string) []byte {
		cmd := exec.Command(bin, append(args, "--nvidia-smi", smi, "--nomad-addr", a.addr, "--nomad-token-env", "GL_IT_TOKEN", "--node", "it")...)
		cmd.Env = append(os.Environ(), "GL_IT_TOKEN="+token)
		out, err := cmd.Output()
		if _, ok := err.(*exec.ExitError); err != nil && !ok {
			t.Fatal(err)
		}
		return out
	}

	var check struct {
		Worst    string
		Findings []struct{ Level, Code, GPU, Message string }
	}
	out := gl(minimal, "check", "--json")
	if err := json.Unmarshal(out, &check); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	on := func(code, uuid string) bool {
		for _, f := range check.Findings {
			if f.Code == code && (uuid == "" || strings.HasSuffix(f.GPU, uuid[:12])) {
				return true
			}
		}
		return false
	}
	for _, want := range []struct{ code, gpu string }{
		{"held", encGPU},            // reserved, and the only tenant is the reservation's
		{"reserved-idle", idleGPU},  // a reservation in another namespace, no process
		{"unreserved-tenant", free}, // enc's process on a GPU Nomad did not give it
		{"unmanaged-tenant", free},  // the rogue container, by NVIDIA_VISIBLE_DEVICES
		{"contended", free},
	} {
		if !on(want.code, want.gpu) {
			t.Errorf("missing %s on %s in\n%s", want.code, want.gpu, out)
		}
	}
	if on("source-unavailable", "") || check.Worst != "BAD" {
		t.Errorf("a token with agent:read and node:read must be enough, worst must be BAD:\n%s", out)
	}
	if bytes.Contains(out, []byte("secret")) || bytes.Contains(out, []byte(minimal)) {
		t.Error("arguments and tokens must never be printed")
	}

	var l struct {
		Entries []struct {
			UUID         string
			Reservations []struct{ AllocID, JobID, Namespace string }
			Tenants      []struct {
				Kind, AllocID, JobName, TaskName, Namespace string
				Reserved                                    bool
				Processes                                   []string
			}
		}
	}
	out = gl(minimal, "ls", "--json")
	if err := json.Unmarshal(out, &l); err != nil || len(l.Entries) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, e := range l.Entries {
		switch e.UUID {
		case encGPU:
			if len(e.Reservations) != 1 || e.Reservations[0].AllocID != enc.ID || len(e.Tenants) != 1 {
				t.Errorf("enc GPU: %+v", e)
			} else if tn := e.Tenants[0]; tn.Kind != "nomad" || !tn.Reserved || tn.AllocID != enc.ID || tn.JobName != "enc" || tn.TaskName != "enc" || tn.Namespace != "default" || tn.Processes[0] != "ffmpeg" {
				t.Errorf("enc tenant: %+v", tn)
			}
		case idleGPU:
			if len(e.Reservations) != 1 || e.Reservations[0].AllocID != idle.ID || e.Reservations[0].Namespace != "video" || len(e.Tenants) != 0 {
				t.Errorf("idle GPU: %+v", e)
			}
		case free:
			if len(e.Reservations) != 0 || len(e.Tenants) != 2 {
				t.Errorf("free GPU: %+v", e)
			}
		}
	}

	// Too little ACL is a finding that names the missing capability, not a crash.
	out = gl(agentOnly, "check", "--json")
	if !bytes.Contains(out, []byte(`"source-unavailable"`)) || !bytes.Contains(out, []byte("node:read")) {
		t.Errorf("a token without node:read:\n%s", out)
	}
	out = gl("", "check", "--json")
	if !bytes.Contains(out, []byte(`"source-unavailable"`)) || !bytes.Contains(out, []byte("agent:read")) {
		t.Errorf("no token on an ACL cluster:\n%s", out)
	}

	if promtool := os.Getenv("PROMTOOL"); promtool != "" {
		metrics(t, bin, smi, a.addr, minimal, promtool)
	}
}

// metrics serves once and hands /metrics to promtool check metrics.
func metrics(t *testing.T, bin, smi, addr, token, promtool string) {
	listen := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "serve", "--listen", listen, "--nvidia-smi", smi, "--nomad-addr", addr, "--nomad-token-env", "GL_IT_TOKEN", "--node", "it")
	cmd.Env = append(os.Environ(), "GL_IT_TOKEN="+token)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var body []byte
	eventually(t, "/metrics", 20*time.Second, func() (bool, string) {
		res, err := http.Get("http://" + listen + "/metrics")
		if err != nil {
			return false, err.Error()
		}
		defer res.Body.Close()
		body, _ = io.ReadAll(res.Body)
		return res.StatusCode == 200, res.Status
	})
	if !bytes.Contains(body, []byte(`gpuledger_up{node="it"} 1`)) {
		t.Errorf("up must be 1:\n%s", body)
	}
	pc := exec.Command(promtool, "check", "metrics")
	pc.Stdin = bytes.NewReader(body)
	if out, err := pc.CombinedOutput(); err != nil {
		t.Errorf("promtool check metrics: %v\n%s", err, out)
	}
}
