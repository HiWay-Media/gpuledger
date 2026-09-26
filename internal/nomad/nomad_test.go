package nomad

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"

	"github.com/hiway-media/gpuledger/internal/testcerts"
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

// mtlsServer is an agent API that requires a client certificate signed by the set's CA.
func mtlsServer(t *testing.T, set testcerts.Set) *httptest.Server {
	pool := x509.NewCertPool()
	ca, _ := os.ReadFile(set.CA)
	pool.AppendCertsFromPEM(ca)
	cert, err := tls.LoadX509KeyPair(set.ServerCert, set.ServerKey)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"stats":{"client":{"node_id":"n-tls"}}}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestMutualTLS(t *testing.T) {
	set, err := testcerts.Write(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := mtlsServer(t, set)
	ctx := context.Background()
	c, err := NewTLSClient(srv.URL, "", TLS{CACert: set.CA, ClientCert: set.ClientCert, ClientKey: set.ClientKey})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := c.NodeID(ctx); err != nil || id != "n-tls" {
		t.Fatalf("with CA and client certificate: %q %v", id, err)
	}
	// A CA directory, as NOMAD_CAPATH names one, and a server name override.
	dir := t.TempDir()
	b, _ := os.ReadFile(set.CA)
	os.WriteFile(filepath.Join(dir, "ca.pem"), b, 0o600)
	named := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	c, _ = NewTLSClient(named, "", TLS{CAPath: dir, ClientCert: set.ClientCert, ClientKey: set.ClientKey, ServerName: "server.global.nomad"})
	if id, err := c.NodeID(ctx); err != nil || id != "n-tls" {
		t.Fatalf("with a CA path and a server name: %q %v", id, err)
	}
	// No client certificate: the handshake fails, and the error says it is TLS.
	c, _ = NewTLSClient(srv.URL, "", TLS{CACert: set.CA})
	if _, err := c.NodeID(ctx); err == nil || !strings.Contains(err.Error(), "tls") {
		t.Fatalf("without a client certificate: %v", err)
	}
	// Not trusting the agent's CA.
	c, _ = NewTLSClient(srv.URL, "", TLS{ClientCert: set.ClientCert, ClientKey: set.ClientKey})
	if _, err := c.NodeID(ctx); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("an unknown CA: %v", err)
	}
	// Files that cannot be read are an error when the client is made, naming the flag.
	for _, bad := range []TLS{{CACert: "/nope/ca.pem"}, {ClientCert: set.ClientCert}, {ClientCert: "/nope/c.pem", ClientKey: "/nope/k.pem"}, {CAPath: t.TempDir()}} {
		if _, err := NewTLSClient(srv.URL, "", bad); err == nil || !strings.Contains(err.Error(), "--nomad-") {
			t.Errorf("%+v: want an error naming the flag, got %v", bad, err)
		}
	}
}

func TestTLSFromTheNomadCLIsVariables(t *testing.T) {
	t.Setenv("NOMAD_CACERT", "/c/ca.pem")
	t.Setenv("NOMAD_CAPATH", "/c/cas")
	t.Setenv("NOMAD_CLIENT_CERT", "/c/cli.pem")
	t.Setenv("NOMAD_CLIENT_KEY", "/c/cli-key.pem")
	t.Setenv("NOMAD_TLS_SERVER_NAME", "server.global.nomad")
	if got := TLSFromEnv(); got != (TLS{CACert: "/c/ca.pem", CAPath: "/c/cas", ClientCert: "/c/cli.pem", ClientKey: "/c/cli-key.pem", ServerName: "server.global.nomad"}) {
		t.Fatalf("%+v", got)
	}
}

// Nomad's own service discovery (1.3+): /v1/service/<name> lists the registrations,
// each with the address and port the task was given.
func TestServices(t *testing.T) {
	var seen *http.Request
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/service/gpuledger", func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.Write([]byte(`[
		 {"ServiceName":"gpuledger","Namespace":"default","NodeID":"n2","Datacenter":"dc1","AllocID":"a2","Address":"10.0.0.5","Port":9877},
		 {"ServiceName":"gpuledger","Namespace":"default","NodeID":"n1","Datacenter":"dc1","AllocID":"a1","Address":"fd00::4","Port":9877}
		]`))
	})
	mux.HandleFunc("/v1/service/none", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("TEST_NOMAD_TOKEN", "s.secret")
	c := NewClient(srv.URL, "TEST_NOMAD_TOKEN")
	svcs, err := c.Services(context.Background(), "gpuledger", "default")
	if err != nil || len(svcs) != 2 || svcs[0].Address != "10.0.0.5" || svcs[1].Port != 9877 || svcs[0].NodeID != "n2" {
		t.Fatalf("%v %+v", err, svcs)
	}
	if seen.URL.Query().Get("namespace") != "default" || seen.Header.Get("X-Nomad-Token") != "s.secret" {
		t.Fatalf("%s %v", seen.URL, seen.Header)
	}
	if svcs, err := c.Services(context.Background(), "none", "default"); err != nil || len(svcs) != 0 {
		t.Fatalf("%v %+v", err, svcs)
	}
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }))
	defer forbidden.Close()
	if _, err := NewClient(forbidden.URL, "").Services(context.Background(), "gpuledger", "default"); err == nil || !strings.Contains(err.Error(), "read-job") {
		t.Fatalf("a 403 names read-job: %v", err)
	}
}
