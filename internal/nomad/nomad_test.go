package nomad

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	res, err := c.Reservations(context.Background(), id)
	if err != nil || len(res) != 1 || res[0].JobID != "gpu-gpud-restreamer" || res[0].Task != "restreamer" || res[0].DeviceIDs[0] != "GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f" {
		t.Fatalf("%v %+v", err, res)
	}
	if id, _ := NewClient(srv.URL, "").NodeID(context.Background()); id != "node-1" || seenToken != "" {
		t.Fatal("no token env → no header")
	}
}
