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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// labels is false on Nomad 1.0, whose docker driver has no extra_labels: the
	// containers then carry the alloc id only, and job, task and namespace are empty.
	labels bool
}

// extraLabels is true from Nomad 1.1, where the docker driver took extra_labels.
func extraLabels(t *testing.T, nomadBin string) bool {
	out, err := exec.Command(nomadBin, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	var major, minor int
	fmt.Sscanf(strings.TrimPrefix(strings.Fields(string(out))[1], "v"), "%d.%d", &major, &minor)
	t.Logf("%s", bytes.TrimSpace(out))
	return major > 1 || minor >= 1
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
func start(t *testing.T, podmanDriver ...string) *agent {
	nomadBin, plugin := os.Getenv("NOMAD_BIN"), os.Getenv("GPULEDGER_DEVICE_PLUGIN")
	if nomadBin == "" || plugin == "" {
		t.Skip("NOMAD_BIN and GPULEDGER_DEVICE_PLUGIN are required")
	}
	// Not t.TempDir: from Nomad 1.5 the task's secrets and private dirs are tmpfs
	// mounts that outlive the agent, and RemoveAll fails on them.
	dir, err := os.MkdirTemp("", "gpuledger-it-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unmountUnder(dir); os.RemoveAll(dir) })
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
	labels := extraLabels(t, nomadBin)
	docker := `
plugin "docker" {
  config {
    extra_labels = ["job_name", "task_group_name", "task_name", "namespace", "node_name"]
  }
}`
	if !labels {
		docker = ""
	}
	// The podman driver, when asked for, without extra_labels: the case where the
	// allocation is only in the container's name.
	for _, drv := range podmanDriver {
		b, err := os.ReadFile(drv)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(plugins, "nomad-driver-podman"), b, 0o755)
		docker += `
plugin "nomad-driver-podman" {
  config {
    socket_path = "unix:///run/podman/podman.sock"
  }
}`
	}
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
%s
`, filepath.Join(dir, "data"), plugins, port, freePort(t), freePort(t), devices, docker)
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
	a := &agent{t: t, addr: fmt.Sprintf("http://127.0.0.1:%d", port), labels: labels}
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

	// The token gpuledger gets: the least the README says it needs. Without read-job
	// on a namespace, Nomad leaves that namespace's allocations out of the node's list
	// without an error — noNS proves gpuledger says so instead of calling them unreserved.
	policy, err := os.ReadFile("../deploy/nomad/gpuledger.policy.hcl")
	if err != nil {
		t.Fatal(err)
	}
	minimal := a.token("gpuledger", string(policy))
	noNS := a.token("no-namespace", `agent { policy = "read" }
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

	type report struct {
		Worst    string
		Findings []struct{ Level, Code, GPU, Message string }
	}
	var check report
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
			} else if tn := e.Tenants[0]; tn.Kind != "nomad" || !tn.Reserved || tn.AllocID != enc.ID || tn.Processes[0] != "ffmpeg" {
				t.Errorf("enc tenant: %+v", tn)
			} else if a.labels && (tn.JobName != "enc" || tn.TaskName != "enc" || tn.Namespace != "default") {
				t.Errorf("enc tenant's extra labels: %+v", tn)
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

	// No read-job: enc's allocation is not returned. The ledger must say it cannot tell,
	// never call the task unreserved.
	var partial report
	out = gl(noNS, "check", "--json")
	if err := json.Unmarshal(out, &partial); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	found := false
	for _, f := range partial.Findings {
		if f.Code == "unreserved-tenant" {
			t.Errorf("an allocation Nomad did not return is not unreserved: %+v", f)
		}
		if f.Code == "source-unavailable" && strings.Contains(f.Message, "read-job") && strings.Contains(f.Message, enc.ID[:8]) {
			found = true
		}
	}
	if !found || partial.Worst != "ERROR" {
		t.Errorf("a token without read-job must be an ERROR naming it:\n%s", out)
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

	// The system job the README tells operators to run is valid on this version.
	validate := exec.Command(os.Getenv("NOMAD_BIN"), "job", "validate", "-var", `datacenters=["dc1"]`, "../deploy/nomad/gpuledger.nomad.hcl")
	validate.Env = append(os.Environ(), "NOMAD_ADDR="+a.addr, "NOMAD_TOKEN="+a.mgmt)
	if out, err := validate.CombinedOutput(); err != nil {
		t.Errorf("nomad job validate deploy/nomad/gpuledger.nomad.hcl: %v\n%s", err, out)
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
	hist := filepath.Join(t.TempDir(), "history.json")
	cmd := exec.CommandContext(ctx, bin, "serve", "--listen", listen, "--interval", "1s", "--history", hist, "--nvidia-smi", smi, "--nomad-addr", addr, "--nomad-token-env", "GL_IT_TOKEN", "--node", "it")
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
	eventually(t, "a since from the history", 20*time.Second, func() (bool, string) {
		res, err := http.Get("http://" + listen + "/metrics")
		if err != nil {
			return false, err.Error()
		}
		defer res.Body.Close()
		body, _ = io.ReadAll(res.Body)
		return bytes.Contains(body, []byte(`gpuledger_gpu_state_since_timestamp_seconds{node="it",gpu=`)) && bytes.Contains(body, []byte(`state="reserved-idle"`)), "no since yet"
	})
	if _, err := os.Stat(hist); err != nil {
		t.Errorf("serve --history writes the file: %v", err)
	}
	for _, want := range []string{`gpuledger_findings{node="it",code="unreserved-tenant",level="BAD"} 1`, `gpuledger_findings{node="it",code="source-unavailable",level="ERROR"} 0`, `gpuledger_worst_level{node="it"} 2`} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("missing %s in /metrics", want)
		}
	}
	if !bytes.Contains(body, []byte(`gpuledger_up{node="it"} 1`)) {
		t.Errorf("up must be 1:\n%s", body)
	}
	// The fleet view over this node's /ledger: the per-job counts come from real
	// allocations, in both namespaces.
	fl, err := exec.Command(bin, "fleet", "ls", "--json", "--targets", listen).Output()
	if err != nil {
		t.Fatalf("fleet ls: %v %s", err, fl)
	}
	var fs struct {
		Summary struct {
			Total struct{ GPUs, Held, ReservedIdle, Unaccounted, Free int }
			Jobs  []struct {
				Namespace, Job string
				Reserved, Held int
			}
		}
	}
	if err := json.Unmarshal(fl, &fs); err != nil {
		t.Fatalf("%v %s", err, fl)
	}
	jobs := map[string][2]int{}
	for _, j := range fs.Summary.Jobs {
		jobs[j.Namespace+"/"+j.Job] = [2]int{j.Reserved, j.Held}
	}
	if tot := fs.Summary.Total; tot.GPUs != 3 || tot.Held != 1 || tot.ReservedIdle != 1 || tot.Unaccounted != 1 || jobs["default/enc"] != [2]int{1, 1} || jobs["video/idle"] != [2]int{1, 0} {
		t.Errorf("fleet ls against the real node:\n%s", fl)
	}

	if promBin := os.Getenv("PROMETHEUS_BIN"); promBin != "" {
		dashboardAgainstPrometheus(t, promBin, listen)
	}

	if consulBin := os.Getenv("CONSUL_BIN"); consulBin != "" {
		viaConsul(t, bin, consulBin, listen)
	}

	pc := exec.Command(promtool, "check", "metrics")
	pc.Stdin = bytes.NewReader(body)
	if out, err := pc.CombinedOutput(); err != nil {
		t.Errorf("promtool check metrics: %v\n%s", err, out)
	}
}

// unmountUnder unmounts, deepest first, whatever Nomad left mounted below dir.
func unmountUnder(dir string) {
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return
	}
	var points []string
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) > 1 && strings.HasPrefix(f[1], dir+"/") {
			points = append(points, f[1])
		}
	}
	for i := len(points) - 1; i >= 0; i-- {
		exec.Command("umount", "-l", points[i]).Run()
	}
}

// viaConsul registers the serving node in a real Consul dev agent, with the HTTP check
// on /healthz the system job declares, and reads the fleet through Consul's health API.
func viaConsul(t *testing.T, bin, consulBin, listen string) {
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, consulBin, "agent", "-dev", "-http-port", fmt.Sprint(port), "-dns-port", "-1", "-grpc-port", "-1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf("http://127.0.0.1:%d", port)
	host, p, _ := net.SplitHostPort(listen)
	reg, _ := json.Marshal(map[string]any{"Name": "gpuledger", "Address": host, "Port": atoi(p), "Check": map[string]any{"HTTP": "http://" + listen + "/healthz", "Interval": "1s"}})
	eventually(t, "Consul registration", 30*time.Second, func() (bool, string) {
		req, _ := http.NewRequest("PUT", addr+"/v1/agent/service/register", bytes.NewReader(reg))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, err.Error()
		}
		res.Body.Close()
		return res.StatusCode == 200, res.Status
	})
	var out []byte
	eventually(t, "the node through Consul's passing filter", 30*time.Second, func() (bool, string) {
		out, _ = exec.Command(bin, "fleet", "ls", "--json", "--consul", addr).Output()
		return bytes.Contains(out, []byte(`"gpus":3`)) && !bytes.Contains(out, []byte(`"unreachable"`)), string(out)
	})
	t.Logf("fleet via Consul: %s", bytes.TrimSpace(out))
}

func atoi(s string) int {
	n := 0
	fmt.Sscan(s, &n)
	return n
}

// TestPodmanAgainstARealNomad runs a task under Nomad's podman driver, with no
// extra_labels, and checks gpuledger finds its allocation from the container's name
// and calls the card held. Skipped where the driver does not load on this Nomad.
func TestPodmanAgainstARealNomad(t *testing.T) {
	drv := os.Getenv("PODMAN_DRIVER")
	if drv == "" {
		t.Skip("PODMAN_DRIVER is required")
	}
	a := start(t, drv)
	usable := false
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end) && !usable; time.Sleep(time.Second) {
		var nodes []struct{ ID string }
		a.do("GET", "/v1/nodes", a.mgmt, nil, &nodes)
		if len(nodes) == 0 {
			continue
		}
		var node struct {
			Drivers map[string]struct{ Detected, Healthy bool }
		}
		a.do("GET", "/v1/node/"+nodes[0].ID, a.mgmt, nil, &node)
		usable = node.Drivers["podman"].Detected && node.Drivers["podman"].Healthy
	}
	if !usable {
		t.Skip("nomad-driver-podman is not detected and healthy on this Nomad version")
	}
	job := gpuJob("pod", "default")
	task := job["Job"].(map[string]any)["TaskGroups"].([]any)[0].(map[string]any)["Tasks"].([]any)[0].(map[string]any)
	task["Driver"] = "podman"
	task["Config"] = map[string]any{"image": "docker.io/library/" + image, "command": "sleep", "args": []string{"3600"}}
	a.must("POST", "/v1/jobs", job, nil)
	t.Cleanup(func() {
		a.do("DELETE", "/v1/job/pod?purge=true", a.mgmt, nil, nil)
		time.Sleep(3 * time.Second)
	})
	pod, gpu := a.running("pod", "default")
	name := "pod-" + pod.ID
	pid := sh(t, "podman", "inspect", "-f", "{{.State.Pid}}", name)
	t.Logf("podman labels of %s: %s", name, sh(t, "podman", "inspect", "-f", "{{json .Config.Labels}}", name))
	cg, _ := os.ReadFile("/proc/" + pid + "/cgroup")
	t.Logf("cgroup of the podman task (pid %s): %s", pid, bytes.TrimSpace(cg))

	dir := t.TempDir()
	var qg strings.Builder
	for i, g := range gpus {
		fmt.Fprintf(&qg, "%d, %s, Fake RTX, 00000000:0%d:00.0, 100, 8192, 5, 1, 40, 30.5, 0, 0\n", i, g, i+1)
	}
	os.WriteFile(filepath.Join(dir, "gpu.csv"), []byte(qg.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "apps.csv"), []byte(fmt.Sprintf("%s, %s, 256, ffmpeg\n", gpu, pid)), 0o644)
	smi := filepath.Join(dir, "nvidia-smi")
	os.WriteFile(smi, []byte(fmt.Sprintf("#!/bin/sh\ncase \"$1\" in --query-gpu=*) cat %q ;; *) cat %q ;; esac\n", filepath.Join(dir, "gpu.csv"), filepath.Join(dir, "apps.csv"))), 0o755)
	bin := os.Getenv("GPULEDGER_BIN")
	if bin == "" {
		bin = filepath.Join(dir, "gpuledger")
		sh(t, "go", "build", "-o", bin, "../cmd/gpuledger")
	}
	policy, _ := os.ReadFile("../deploy/nomad/gpuledger.policy.hcl")
	tok := a.token("gpuledger", string(policy))
	cmd := exec.Command(bin, "ls", "--json", "--nvidia-smi", smi, "--podman", "unix:///run/podman/podman.sock", "--nomad-addr", a.addr, "--nomad-token-env", "GL_IT_TOKEN", "--node", "it")
	cmd.Env = append(os.Environ(), "GL_IT_TOKEN="+tok)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var l struct {
		Entries []struct {
			UUID, State string
			Tenants     []struct {
				Kind, AllocID           string
				Reserved, AllocFromName bool
			}
		}
		Errors []string
	}
	if err := json.Unmarshal(out, &l); err != nil || len(l.Errors) != 0 {
		t.Fatalf("%v %s", err, out)
	}
	for _, e := range l.Entries {
		if e.UUID != gpu {
			continue
		}
		if e.State != "held" || len(e.Tenants) != 1 || e.Tenants[0].Kind != "nomad" || !e.Tenants[0].Reserved || e.Tenants[0].AllocID != pod.ID || !e.Tenants[0].AllocFromName {
			t.Errorf("the podman task's GPU:\n%s", out)
		}
		return
	}
	t.Errorf("no entry for %s:\n%s", gpu, out)
}

// dashboardAgainstPrometheus scrapes the node with a real Prometheus and runs every
// dashboard query and every alert expression: each must execute, every panel but the
// ones the fake driver cannot feed must return data, and the Nomad job must survive
// ingestion under its own label (Prometheus renames a metric label called job).
func dashboardAgainstPrometheus(t *testing.T, promBin, listen string) {
	dir := t.TempDir()
	port := freePort(t)
	cfg := fmt.Sprintf("global:\n  scrape_interval: 1s\nscrape_configs:\n  - job_name: gpuledger\n    static_configs:\n      - targets: [%q]\n", listen)
	os.WriteFile(filepath.Join(dir, "prometheus.yml"), []byte(cfg), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, promBin, "--config.file", filepath.Join(dir, "prometheus.yml"), "--storage.tsdb.path", filepath.Join(dir, "data"), "--web.listen-address", fmt.Sprintf("127.0.0.1:%d", port))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	query := func(expr string) (string, int, error) {
		res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/query?query=%s", port, url.QueryEscape(expr)))
		if err != nil {
			return "", 0, err
		}
		defer res.Body.Close()
		var r struct {
			Status, Error string
			Data          struct{ Result []json.RawMessage }
		}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			return "", 0, err
		}
		return r.Status + r.Error, len(r.Data.Result), nil
	}
	eventually(t, "Prometheus scraping gpuledger", 60*time.Second, func() (bool, string) {
		st, n, err := query(`gpuledger_gpu_state_since_timestamp_seconds{state="reserved-idle"}`)
		return err == nil && n > 0, fmt.Sprint(st, n, err)
	})
	if _, n, _ := query(`gpuledger_tenant_memory_bytes{nomad_job="enc"}`); n == 0 {
		t.Error("the Nomad job does not survive ingestion under nomad_job")
	}
	b, err := os.ReadFile("../deploy/grafana/gpuledger.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Panels []struct {
			Title   string
			Targets []struct{ Expr string }
		}
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	// The fake nvidia-smi answers none of the optional thermal fields.
	mayBeEmpty := func(expr string) bool { return strings.Contains(expr, "gpuledger_gpu_thermal_") }
	for _, p := range d.Panels {
		for _, tg := range p.Targets {
			expr := strings.ReplaceAll(tg.Expr, "$node", ".*")
			st, n, err := query(expr)
			if err != nil || st != "success" {
				t.Errorf("panel %q: %s: %v %s", p.Title, expr, err, st)
			} else if n == 0 && !mayBeEmpty(expr) {
				t.Errorf("panel %q returns no data: %s", p.Title, expr)
			}
		}
	}
	rules, _ := os.ReadFile("../deploy/prometheus/gpuledger.rules.yml")
	for _, m := range regexp.MustCompile(`(?m)^\s+expr: (.+)$`).FindAllStringSubmatch(string(rules), -1) {
		if st, _, err := query(m[1]); err != nil || st != "success" {
			t.Errorf("rule %s: %v %s", m[1], err, st)
		}
	}
	if _, n, _ := query(`gpuledger_findings{code="unreserved-tenant"} > 0`); n == 0 {
		t.Error("GPULedgerUnreservedTenant's expression must match the unreserved tenant the test plants")
	}
}
