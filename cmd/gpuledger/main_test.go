package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cidA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const cidB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// fakes: a Docker engine with the encoding container (unmanaged) and a Nomad restreamer,
// and a Nomad agent that reserved gpu0 for the restreamer and gpu1 for a worker.
func fakes(t *testing.T) (docker, nomadURL string) {
	dm := http.NewServeMux()
	dm.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"Id":"` + cidA + `"},{"Id":"` + cidB + `"}]`))
	})
	dm.HandleFunc("/containers/"+cidA+"/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Id":"` + cidA + `","Name":"/gpu-d-new-c0","Config":{"Image":"encoding:handbrake-4.1.3-prod","Labels":{},"Env":["NVIDIA_VISIBLE_DEVICES=GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"]},"HostConfig":{}}`))
	})
	dm.HandleFunc("/containers/"+cidB+"/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Id":"` + cidB + `","Name":"/restreamer-3e5d2f75-abcd","Config":{"Image":"restreamer:4.1.2","Labels":{"com.hashicorp.nomad.allocation_id":"3e5d2f75-1111-2222-3333-444444444444","com.hashicorp.nomad.job_name":"gpu-gpud-restreamer","com.hashicorp.nomad.task_name":"restreamer"},"Env":[]},"HostConfig":{}}`))
	})
	ds := httptest.NewServer(dm)
	nm := http.NewServeMux()
	nm.HandleFunc("/v1/agent/self", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"stats":{"client":{"node_id":"n1"}}}`)) })
	nm.HandleFunc("/v1/node/n1/allocations", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"ID":"3e5d2f75-1111-2222-3333-444444444444","JobID":"gpu-gpud-restreamer","ClientStatus":"running","AllocatedResources":{"Tasks":{"restreamer":{"Devices":[{"Type":"gpu","Vendor":"nvidia","DeviceIDs":["GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"]}]}}}},{"ID":"77777777-0000-0000-0000-000000000000","JobID":"tngrm-video-worker-gpud","ClientStatus":"running","AllocatedResources":{"Tasks":{"worker":{"Devices":[{"Type":"gpu","Vendor":"nvidia","DeviceIDs":["GPU-ac81e44d-1234-4d1e-9d53-abcdefabcdef"]}]}}}}]`))
	})
	ns := httptest.NewServer(nm)
	t.Cleanup(ds.Close)
	t.Cleanup(ns.Close)
	return ds.URL, ns.URL
}

func build(t *testing.T) string {
	bin := filepath.Join(t.TempDir(), "gpuledger")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, bin string, args ...string) (string, string, int) {
	docker, nomadURL := fakes(t)
	root, _ := filepath.Abs("../../testdata")
	base := []string{"--nvidia-smi", filepath.Join(root, "fake-nvidia-smi.sh"), "--proc", filepath.Join(root, "proc"), "--docker", docker, "--nomad-addr", nomadURL, "--node", "gpud"}
	cmd := exec.Command(bin, append([]string{args[0]}, append(base, args[1:]...)...)...)
	cmd.Env = append(os.Environ(), "NOMAD_TOKEN=s.test")
	stdout, err := cmd.Output()
	code := 0
	stderr := ""
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
		stderr = string(ee.Stderr)
	} else if err != nil {
		t.Fatal(err)
	}
	return string(stdout), stderr, code
}

func TestLsJoinsEverything(t *testing.T) {
	bin := build(t)
	out, _, code := run(t, bin, "ls")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"gpuledger · gpud · 2 GPU(s)", "gpu-gpud-restreamer/restreamer", "docker:gpu-d-new-c0 (2048 MiB) !unmanaged", "nomad:gpu-gpud-restreamer/restreamer (1024 MiB)", "host:python3 (48 MiB) !unmanaged", "tngrm-video-worker-gpud/worker"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret.mkv") || strings.Contains(out, "s.test") {
		t.Fatal("arguments and tokens must never be printed")
	}
	js, _, _ := run(t, bin, "ls", "--json")
	var l struct {
		Entries []struct {
			Tenants []struct{ Kind string } `json:"tenants"`
		} `json:"entries"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(js), &l); err != nil || len(l.Entries) != 2 || len(l.Entries[0].Tenants) != 3 || len(l.Errors) != 0 {
		t.Fatalf("%v %s", err, js)
	}
}

func TestCheckFindingsAndExitPolicy(t *testing.T) {
	bin := build(t)
	out, _, code := run(t, bin, "check")
	if code != 0 {
		t.Fatalf("default policy exits 0, got %d", code)
	}
	for _, want := range []string{"BAD", "unmanaged-tenant", "contended", "reserved-idle", "4 findings: 0 OK, 2 WARN, 2 BAD, 0 ERROR"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "unreserved-tenant") {
		t.Fatalf("the restreamer is reserved: %s", out)
	}
	_, _, code = run(t, bin, "check", "--exit-on", "bad")
	if code != 2 {
		t.Fatalf("--exit-on bad → 2, got %d", code)
	}
	_, _, code = run(t, bin, "check", "--exit-on", "bad", "--allow-unmanaged")
	if code != 0 {
		t.Fatalf("with unmanaged allowed the worst is WARN → 0 under --exit-on bad, got %d", code)
	}
	js, _, _ := run(t, bin, "check", "--json")
	var r struct {
		Worst    string `json:"worst"`
		Findings []struct{ Code string }
	}
	if err := json.Unmarshal([]byte(js), &r); err != nil || r.Worst != "BAD" || len(r.Findings) < 4 {
		t.Fatalf("%v %s", err, js)
	}
}

func TestSourcesDownAreFindingsNotCrashes(t *testing.T) {
	bin := build(t)
	root, _ := filepath.Abs("../../testdata")
	cmd := exec.Command(bin, "check", "--json", "--nvidia-smi", filepath.Join(root, "fake-nvidia-smi.sh"), "--proc", filepath.Join(root, "proc"), "--docker", "http://127.0.0.1:9", "--nomad-addr", "http://127.0.0.1:9", "--node", "gpud")
	out, _ := cmd.Output()
	if !strings.Contains(string(out), `"source-unavailable"`) || !strings.Contains(string(out), `"worst":"ERROR"`) {
		t.Fatalf("%s", out)
	}
	cmd = exec.Command(bin, "ls", "--json", "--nvidia-smi", "/definitely/missing", "--no-docker", "--no-nomad", "--node", "gpud")
	out, _ = cmd.Output()
	if !strings.Contains(string(out), `"errors":["nvidia-smi query-gpu`) {
		t.Fatalf("%s", out)
	}
}

func TestServeExposesMetricsLedgerAndFindings(t *testing.T) {
	bin := build(t)
	docker, nomadURL := fakes(t)
	root, _ := filepath.Abs("../../testdata")
	listen := freeAddr(t)
	cmd := exec.Command(bin, "serve", "--listen", listen, "--interval", "1s", "--nvidia-smi", filepath.Join(root, "fake-nvidia-smi.sh"), "--proc", filepath.Join(root, "proc"), "--docker", docker, "--nomad-addr", nomadURL, "--node", "gpud")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var body string
	for i := 0; i < 50; i++ {
		res, err := http.Get("http://" + listen + "/metrics")
		if err == nil {
			b := make([]byte, 1<<16)
			n, _ := res.Body.Read(b)
			body = string(b[:n])
			res.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, want := range []string{`gpuledger_up{node="gpud"} 1`, `gpuledger_gpu_tenants{node="gpud",gpu="0",uuid="GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"} 3`, `gpuledger_gpu_reservations{node="gpud",gpu="1",uuid="GPU-ac81e44d-1234-4d1e-9d53-abcdefabcdef"} 1`, `kind="docker",container="gpu-d-new-c0"`, `gpuledger_gpu_thermal_margin_celsius{node="gpud",gpu="0",uuid="GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"} 19`, `gpuledger_gpu_thermal_slowdown{node="gpud",gpu="1",uuid="GPU-ac81e44d-1234-4d1e-9d53-abcdefabcdef"} 0`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in\n%s", want, body)
		}
	}
	for _, path := range []string{"/ledger", "/findings", "/healthz"} {
		res, err := http.Get("http://" + listen + path)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, res)
		}
		res.Body.Close()
	}
}

func TestVersionAndUsage(t *testing.T) {
	bin := build(t)
	out, _ := exec.Command(bin, "version").Output()
	if !strings.HasPrefix(string(out), "gpuledger dev") {
		t.Fatalf("%s", out)
	}
	if err := exec.Command(bin, "bogus").Run(); err == nil {
		t.Fatal("unknown command must fail")
	}
}

// Two nodes served by the real binary, one dead, found through a fake Consul.
func TestFleetOverRealServeProcesses(t *testing.T) {
	bin := build(t)
	docker, nomadURL := fakes(t)
	root, _ := filepath.Abs("../../testdata")
	var addrs []string
	for _, node := range []string{"gpud", "gpue"} {
		addr := freeAddr(t)
		cmd := exec.Command(bin, "serve", "--listen", addr, "--nvidia-smi", filepath.Join(root, "fake-nvidia-smi.sh"), "--proc", filepath.Join(root, "proc"), "--docker", docker, "--nomad-addr", nomadURL, "--node", node)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill() })
		addrs = append(addrs, addr)
		for j := 0; j < 50; j++ {
			if res, err := http.Get("http://" + addr + "/healthz"); err == nil {
				res.Body.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	consul := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health/service/gpuledger" {
			http.NotFound(w, r)
			return
		}
		port := func(a string) string { return a[strings.LastIndex(a, ":")+1:] }
		fmt.Fprintf(w, `[{"Node":{"Node":"gpud","Address":"127.0.0.1"},"Service":{"Port":%s}},{"Node":{"Node":"gpue","Address":"127.0.0.1"},"Service":{"Port":%s}},{"Node":{"Node":"gpuf","Address":"127.0.0.1"},"Service":{"Port":9}}]`, port(addrs[0]), port(addrs[1]))
	}))
	defer consul.Close()

	out, err := exec.Command(bin, "fleet", "ls", "--consul", consul.URL, "--timeout", "2s").Output()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, want := range []string{"gpuledger fleet · 3 node(s), 1 unreachable · 4 GPU(s)", "│ gpud  │ 2    │ 0    │ 1             │ 1           │ 0", "│ gpuf  │ —", "│ fleet │ 4    │ 0    │ 2             │ 2           │ 0", "default/gpu-gpud-restreamer", "tngrm-video-worker-gpud"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	cmd := exec.Command(bin, "fleet", "check", "--json", "--exit-on", "bad", "--targets", strings.Join(addrs, ","))
	js, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	var r struct {
		Worst    string
		Findings []struct{ Level, Code, Node string }
	}
	if err := json.Unmarshal(js, &r); err != nil || r.Worst != "BAD" || code != 2 {
		t.Fatalf("worst BAD, exit 2 under --exit-on bad: %v %d %s", err, code, js)
	}
	nodes := map[string]bool{}
	for _, f := range r.Findings {
		nodes[f.Node] = true
	}
	if !nodes["gpud"] || !nodes["gpue"] {
		t.Fatalf("findings from both nodes: %s", js)
	}

	// Consul down is a finding, not a crash.
	out, _ = exec.Command(bin, "fleet", "check", "--consul", "http://127.0.0.1:9").Output()
	if !strings.Contains(string(out), "source-unavailable") || !strings.Contains(string(out), "consul") {
		t.Fatalf("%s", out)
	}
}

// serve --history writes the file; /ledger carries stateSince; check --history reads it,
// says for how long, and never writes it.
func TestHistoryFromServeToCheck(t *testing.T) {
	bin := build(t)
	docker, nomadURL := fakes(t)
	root, _ := filepath.Abs("../../testdata")
	hist := filepath.Join(t.TempDir(), "state", "history.json")
	src := []string{"--nvidia-smi", filepath.Join(root, "fake-nvidia-smi.sh"), "--proc", filepath.Join(root, "proc"), "--docker", docker, "--nomad-addr", nomadURL, "--node", "gpud", "--history", hist, "--interval", "1s"}
	addr := freeAddr(t)
	srv := exec.Command(bin, append([]string{"serve", "--listen", addr}, src...)...)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Process.Kill()
	var body []byte
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		res, err := http.Get("http://" + addr + "/ledger")
		if err != nil {
			continue
		}
		body, _ = io.ReadAll(res.Body)
		res.Body.Close()
		if strings.Contains(string(body), `"stateSince"`) {
			break
		}
	}
	if !strings.Contains(string(body), `"state":"reserved-idle","stateSince"`) {
		t.Fatalf("/ledger with history: %s", body)
	}
	time.Sleep(1500 * time.Millisecond)
	st, err := os.Stat(hist)
	if err != nil {
		t.Fatalf("serve writes the history: %v", err)
	}
	srv.Process.Kill()
	srv.Wait()

	cmd := exec.Command(bin, append([]string{"check"}, src...)...)
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "reserved-idle") || !strings.Contains(string(out), "holds it, for ") {
		t.Fatalf("check --history says for how long:\n%s", out)
	}
	if st2, _ := os.Stat(hist); !st2.ModTime().Equal(st.ModTime()) {
		t.Fatal("check must not write the history")
	}
	os.WriteFile(hist, []byte("{broken"), 0o644)
	out, _ = exec.Command(bin, append([]string{"check", "--json"}, src...)...).Output()
	if !strings.Contains(string(out), `"source-unavailable"`) || !strings.Contains(string(out), "history") {
		t.Fatalf("a corrupt history is a finding: %s", out)
	}
}

// A Nomad podman task without extra_labels, next to the Docker engine: the allocation
// comes from the container's name and Nomad confirms it.
func TestPodmanNextToDocker(t *testing.T) {
	bin := build(t)
	docker, _ := fakes(t)
	const alloc = "9eab414d-13fc-1cbe-c7ac-e9e1498a3fb3"
	cid := strings.Repeat("d", 64)
	pm := http.NewServeMux()
	pm.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, `[{"Id":"%s"}]`, cid) })
	pm.HandleFunc("/containers/"+cid+"/json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"Id":"%s","Name":"enc-%s","Config":{"Image":"docker.io/library/busybox:1.36","Labels":{},"Env":[]},"HostConfig":{}}`, cid, alloc)
	})
	podman := httptest.NewServer(pm)
	defer podman.Close()
	nm := http.NewServeMux()
	nm.HandleFunc("/v1/agent/self", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"stats":{"client":{"node_id":"n1"}}}`)) })
	nm.HandleFunc("/v1/node/n1/allocations", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `[{"ID":"%s","JobID":"enc","Namespace":"default","ClientStatus":"running","AllocatedResources":{"Tasks":{"enc":{"Devices":[{"Vendor":"nvidia","Type":"gpu","DeviceIDs":["GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"]}]}}}}]`, alloc)
	})
	nomadSrv := httptest.NewServer(nm)
	defer nomadSrv.Close()
	dir := t.TempDir()
	root, _ := filepath.Abs("../../testdata")
	smi := filepath.Join(dir, "nvidia-smi")
	os.WriteFile(smi, []byte("#!/bin/sh\ncase \"$1\" in --query-gpu=index,uuid,*) cat "+filepath.Join(root, "query-gpu.csv")+" ;; --query-gpu=*) exit 2 ;; *) echo 'GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f, 7070, 512, ffmpeg' ;; esac\n"), 0o755)

	out, err := exec.Command(bin, "ls", "--json", "--nvidia-smi", smi, "--proc", filepath.Join(root, "proc"), "--docker", docker, "--podman", podman.URL, "--nomad-addr", nomadSrv.URL, "--node", "gpud").Output()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var l struct {
		Entries []struct {
			State   string
			Tenants []struct {
				Kind, AllocID, TaskName string
				Reserved, AllocFromName bool
			}
		}
		Errors []string
	}
	if err := json.Unmarshal(out, &l); err != nil || len(l.Errors) != 0 {
		t.Fatalf("%v %s", err, out)
	}
	// gpu0 also has the hand-started encoder from the Docker fake (by its environment),
	// so the card is unaccounted; the podman task itself is Nomad's and reserved.
	found := false
	for _, tn := range l.Entries[0].Tenants {
		if tn.AllocID == alloc {
			found = tn.Kind == "nomad" && tn.Reserved && tn.AllocFromName && tn.TaskName == "enc"
		}
	}
	if !found || len(l.Entries[0].Tenants) != 2 {
		t.Fatalf("the podman task is Nomad's and reserved: %s", out)
	}

	// An explicit --podman that cannot be read is a source error; the default, when
	// the socket does not exist, is simply no Podman.
	out, _ = exec.Command(bin, "ls", "--json", "--nvidia-smi", smi, "--proc", filepath.Join(root, "proc"), "--docker", docker, "--podman", "http://127.0.0.1:9", "--no-nomad", "--node", "gpud").Output()
	if !strings.Contains(string(out), `"podman: `) {
		t.Fatalf("explicit podman down: %s", out)
	}
}

// freeAddr is a loopback address nothing listens on — never a fixed port, which a
// gpuledger serve started by hand would already hold.
func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}
