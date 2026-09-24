package main

import (
	"encoding/json"
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
	cmd := exec.Command(bin, "serve", "--listen", "127.0.0.1:19877", "--interval", "1s", "--nvidia-smi", filepath.Join(root, "fake-nvidia-smi.sh"), "--proc", filepath.Join(root, "proc"), "--docker", docker, "--nomad-addr", nomadURL, "--node", "gpud")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var body string
	for i := 0; i < 50; i++ {
		res, err := http.Get("http://127.0.0.1:19877/metrics")
		if err == nil {
			b := make([]byte, 1<<16)
			n, _ := res.Body.Read(b)
			body = string(b[:n])
			res.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, want := range []string{`gpuledger_up{node="gpud"} 1`, `gpuledger_gpu_tenants{node="gpud",gpu="0",uuid="GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"} 3`, `gpuledger_gpu_reservations{node="gpud",gpu="1",uuid="GPU-ac81e44d-1234-4d1e-9d53-abcdefabcdef"} 1`, `kind="docker",container="gpu-d-new-c0"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in\n%s", want, body)
		}
	}
	for _, path := range []string{"/ledger", "/findings", "/healthz"} {
		res, err := http.Get("http://127.0.0.1:19877" + path)
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
