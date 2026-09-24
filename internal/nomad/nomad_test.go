package nomad

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNodeIDAndReservations(t *testing.T) {
	var seenToken string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent/self", func(w http.ResponseWriter, r *http.Request) {
		seenToken = r.Header.Get("X-Nomad-Token")
		w.Write([]byte(`{"stats":{"client":{"node_id":"node-1"}}}`))
	})
	mux.HandleFunc("/v1/node/node-1/allocations", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[
		 {"ID":"alloc-run","JobID":"gpu-gpud-restreamer","Namespace":"default","TaskGroup":"g","ClientStatus":"running","AllocatedResources":{"Tasks":{"restreamer":{"Devices":[{"Vendor":"nvidia","Type":"gpu","Name":"Quadro RTX 4000","DeviceIDs":["GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"]}]}}}},
		 {"ID":"alloc-dead","JobID":"old","Namespace":"default","TaskGroup":"g","ClientStatus":"complete","AllocatedResources":{"Tasks":{"t":{"Devices":[{"Vendor":"nvidia","Type":"gpu","DeviceIDs":["GPU-x"]}]}}}},
		 {"ID":"alloc-nogpu","JobID":"web","Namespace":"default","TaskGroup":"g","ClientStatus":"running","AllocatedResources":{"Tasks":{"t":{"Devices":[]}}}}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("TEST_NOMAD_TOKEN", "s.secret")
	c := NewClient(srv.URL, "TEST_NOMAD_TOKEN")
	id, err := c.NodeID(context.Background())
	if err != nil || id != "node-1" || seenToken != "s.secret" {
		t.Fatalf("%v %q token=%q", err, id, seenToken)
	}
	res, allocs, err := c.Reservations(context.Background(), id)
	if len(allocs) != 3 || allocs["alloc-dead"] != "default" {
		t.Fatalf("every returned allocation is visible, whatever its status: %v", allocs)
	}
	if err != nil || len(res) != 1 || res[0].JobID != "gpu-gpud-restreamer" || res[0].Task != "restreamer" || res[0].DeviceIDs[0] != "GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f" {
		t.Fatalf("%v %+v", err, res)
	}
	if id, _ := NewClient(srv.URL, "").NodeID(context.Background()); id != "node-1" || seenToken != "" {
		t.Fatal("no token env → no header")
	}
}

func serveAllocs(t *testing.T, status int, body string) *Client {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent/self", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(`{"stats":{"client":{"node_id":"n"}}}`))
	})
	mux.HandleFunc("/v1/node/n/allocations", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "")
}

// Only NVIDIA GPUs are the ledger's business: another plugin's devices (the example
// plugin's files, an AMD card) must not become reservations the join can never match.
func TestReservationsKeepNvidiaGPUsOnlyAndTheNamespace(t *testing.T) {
	c := serveAllocs(t, 200, `[
	 {"ID":"a1","JobID":"enc","Namespace":"video","TaskGroup":"g","ClientStatus":"pending","AllocatedResources":{"Tasks":{"t":{"Devices":[
	   {"Vendor":"nvidia","Type":"gpu","Name":"L4","DeviceIDs":["GPU-1"]},
	   {"Vendor":"nomad","Type":"file","Name":"mock","DeviceIDs":["device01"]},
	   {"Vendor":"amd","Type":"gpu","Name":"MI210","DeviceIDs":["0000:03:00.0"]}]}}}},
	 {"ID":"a2","JobID":"old","ClientStatus":"lost","AllocatedResources":{"Tasks":{"t":{"Devices":[{"Vendor":"nvidia","Type":"gpu","DeviceIDs":["GPU-2"]}]}}}},
	 {"ID":"a3","JobID":"nores","ClientStatus":"running","AllocatedResources":null}
	]`)
	res, allocs, err := c.Reservations(context.Background(), "n")
	if allocs["a1"] != "video" || len(allocs) != 3 {
		t.Fatalf("visible allocations: %v", allocs)
	}
	if err != nil || len(res) != 1 || res[0].Namespace != "video" || res[0].Status != "pending" || res[0].DeviceIDs[0] != "GPU-1" {
		t.Fatalf("%v %+v", err, res)
	}
}

// A 403 is the most likely failure on a cluster with ACLs; the error must say what the
// token lacks, not only the status code.
func TestForbiddenSaysWhichCapabilityIsMissing(t *testing.T) {
	_, err := serveAllocs(t, 403, `Permission denied`).NodeID(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "agent:read") {
		t.Fatalf("%v", err)
	}
	c := serveAllocs(t, 403, `Permission denied`)
	if _, _, err := c.Reservations(context.Background(), "n"); err == nil || !strings.Contains(err.Error(), "node:read") {
		t.Fatalf("%v", err)
	}
}
