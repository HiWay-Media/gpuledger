package containers

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerIDOfReadsCgroupV1AndV2AndHostProcesses(t *testing.T) {
	root := "../../testdata/proc"
	if got := ContainerIDOf(root, 4242); got != strings.Repeat("a", 64) {
		t.Fatalf("v2 scope: %q", got)
	}
	if got := ContainerIDOf(root, 5151); got != strings.Repeat("b", 64) {
		t.Fatalf("v1 docker path: %q", got)
	}
	if got := ContainerIDOf(root, 6060); got != "" {
		t.Fatalf("host process: %q", got)
	}
	if got := ContainerIDOf(root, 1); got != "" {
		t.Fatalf("missing pid: %q", got)
	}
}

func dockerFake() *httptest.Server {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"Id":"` + a + `"},{"Id":"` + b + `"}]`))
	})
	mux.HandleFunc("/containers/"+a+"/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Id":"` + a + `","Name":"/gpu-d-new-c0","Config":{"Image":"encoding:handbrake-4.1.3-prod","Labels":{"maintainer":"x"},"Env":["SECRET=hunter2","NVIDIA_VISIBLE_DEVICES=GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"]},"HostConfig":{"Runtime":"nvidia","DeviceRequests":[{"Driver":"nvidia","Count":0,"DeviceIDs":["0"],"Capabilities":[["gpu"]]}]}}`))
	})
	mux.HandleFunc("/containers/"+b+"/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Id":"` + b + `","Name":"/restreamer-3e5d2f75-abcd","Config":{"Image":"restreamer:4.1.2","Labels":{"com.hashicorp.nomad.allocation_id":"3e5d2f75-1111-2222-3333-444444444444","com.hashicorp.nomad.job_name":"gpu-gpud-restreamer","com.hashicorp.nomad.task_name":"restreamer","com.hashicorp.nomad.namespace":"default"},"Env":["PASSWORD=nope"]},"HostConfig":{"DeviceRequests":[{"Driver":"nvidia","Count":-1,"DeviceIDs":null}]}}`))
	})
	return httptest.NewServer(mux)
}

func TestClientListAndLabels(t *testing.T) {
	srv := dockerFake()
	defer srv.Close()
	c := NewClient(srv.URL)
	list, err := c.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %d", err, len(list))
	}
	var enc, res Container
	for _, ct := range list {
		if ct.Name == "gpu-d-new-c0" {
			enc = ct
		} else {
			res = ct
		}
	}
	if enc.NomadManaged() || enc.GPUs[0] != "GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f" || enc.Image != "encoding:handbrake-4.1.3-prod" {
		t.Fatalf("encoding: %+v", enc)
	}
	if !res.NomadManaged() || res.AllocID != "3e5d2f75-1111-2222-3333-444444444444" || res.JobName != "gpu-gpud-restreamer" || res.TaskName != "restreamer" || len(res.GPUs) != 1 || res.GPUs[0] != "all" {
		t.Fatalf("restreamer: %+v", res)
	}
	for _, ct := range list {
		for k := range ct.Labels {
			if !strings.HasPrefix(k, "com.hashicorp.nomad.") {
				t.Fatalf("only Nomad labels are kept, got %s", k)
			}
		}
	}
	if _, err := c.Inspect(context.Background(), strings.Repeat("c", 64)); err == nil {
		t.Fatal("unknown container must error")
	}
}

func TestInspectOfAShortIDFailsWithoutPanicking(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := NewClient(srv.URL).Inspect(context.Background(), "abc"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("want an HTTP 404 error, got %v", err)
	}
}

// The default endpoint on every node is the unix socket; the test serves the Engine API
// on one so that transport is exercised, not only the http:// form.
func TestClientOverTheUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	id := strings.Repeat("c", 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`[{"Id":"` + id + `"},{"Id":"gone"}]`)) })
	mux.HandleFunc("/containers/"+id+"/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Id":"` + id + `","Name":"/enc","Config":{"Image":"x","Labels":{"com.hashicorp.nomad.alloc_id":"a1","other":"dropped"},"Env":["SECRET=1","NVIDIA_VISIBLE_DEVICES=GPU-1,GPU-2"]},"HostConfig":{}}`))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()
	list, err := NewClient("unix://" + sock).List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("a container that vanished between list and inspect is skipped: %v %+v", err, list)
	}
	c := list[0]
	if c.Name != "enc" || c.AllocID != "a1" || len(c.GPUs) != 2 || c.Labels["other"] != "" || len(c.Labels) != 1 {
		t.Fatalf("%+v", c)
	}
	if _, err := NewClient("unix://" + filepath.Join(t.TempDir(), "none.sock")).List(context.Background()); err == nil {
		t.Fatal("a missing socket is an error")
	}
}
