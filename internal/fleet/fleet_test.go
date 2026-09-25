package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nomad"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

func gpu(i int, uuid string, used, total int) nvidia.GPU {
	return nvidia.GPU{Index: i, UUID: uuid, Model: "Quadro RTX 4000", MemoryUsedMiB: used, MemoryTotalMiB: total}
}

// gpud: gpu0 held by the restreamer, gpu1 reserved for the worker and idle.
// gpue: gpu0 the hand-started encoder (unaccounted), gpu1 free.
func farm() (gpud, gpue ledger.Ledger) {
	res := func(alloc, ns, job string, ids ...string) nomad.Reservation {
		return nomad.Reservation{AllocID: alloc, Namespace: ns, JobID: job, Task: "t", Status: "running", DeviceIDs: ids}
	}
	gpud = ledger.Ledger{Node: "gpud", NomadRead: true, Entries: []ledger.Entry{
		{GPU: gpu(0, "GPU-d0", 1024, 8192), Reservations: []nomad.Reservation{res("a1", "default", "restreamer", "GPU-d0")}, Tenants: []ledger.Tenant{{Kind: ledger.KindNomad, AllocID: "a1", JobName: "restreamer", Reserved: true, AllocVisible: true, UsedMemoryMiB: 1024}}},
		{GPU: gpu(1, "GPU-d1", 0, 8192), Reservations: []nomad.Reservation{res("a2", "video", "worker", "GPU-d1")}},
	}}
	gpue = ledger.Ledger{Node: "gpue", NomadRead: true, Entries: []ledger.Entry{
		{GPU: gpu(0, "GPU-e0", 2048, 8192), Tenants: []ledger.Tenant{{Kind: ledger.KindDocker, Container: "gpu-e-new-c0", UsedMemoryMiB: 2048}}},
		{GPU: gpu(1, "GPU-e1", 0, 8192)},
	}}
	return
}

func TestStaticTargets(t *testing.T) {
	ts, err := Static("gpud:9877, http://10.0.0.5:9877/ ,[fd00::1]:9877")
	if err != nil || len(ts) != 3 {
		t.Fatalf("%v %+v", err, ts)
	}
	want := []Target{{Node: "gpud", URL: "http://gpud:9877"}, {Node: "10.0.0.5", URL: "http://10.0.0.5:9877"}, {Node: "fd00::1", URL: "http://[fd00::1]:9877"}}
	for i := range want {
		if ts[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, ts[i], want[i])
		}
	}
	for _, bad := range []string{"", " , ", "gpud", "ftp://gpud:1"} {
		if _, err := Static(bad); err == nil {
			t.Errorf("Static(%q) must fail", bad)
		}
	}
}

// Consul's health endpoint, passing instances only, the service address preferred to
// the node's; the token goes in a header, never in the URL.
func TestConsulTargets(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.Write([]byte(`[
		 {"Node":{"Node":"gpue","Address":"10.0.0.5"},"Service":{"Address":"","Port":9877}},
		 {"Node":{"Node":"gpud","Address":"10.0.0.4"},"Service":{"Address":"10.1.0.4","Port":9877}}
		]`))
	}))
	defer srv.Close()
	t.Setenv("TEST_CONSUL_TOKEN", "c.secret")
	ts, err := NewConsul(strings.TrimPrefix(srv.URL, "http://"), "TEST_CONSUL_TOKEN").Targets(context.Background(), "gpuledger")
	if err != nil || len(ts) != 2 {
		t.Fatalf("%v %+v", err, ts)
	}
	if seen.URL.Path != "/v1/health/service/gpuledger" || seen.URL.Query().Get("passing") != "true" || seen.Header.Get("X-Consul-Token") != "c.secret" || strings.Contains(seen.URL.RawQuery, "secret") {
		t.Fatalf("request: %s %v", seen.URL, seen.Header)
	}
	if ts[0] != (Target{Node: "gpud", URL: "http://10.1.0.4:9877"}) || ts[1] != (Target{Node: "gpue", URL: "http://10.0.0.5:9877"}) {
		t.Fatalf("sorted by node, service address first: %+v", ts)
	}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) })
	if _, err := NewConsul(srv.URL, "").Targets(context.Background(), "gpuledger"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a 403 is an error: %v", err)
	}
}

func serveLedger(t *testing.T, l ledger.Ledger) Target {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ledger" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(l)
	}))
	t.Cleanup(srv.Close)
	return Target{Node: "addr-" + l.Node, URL: srv.URL}
}

func TestFetchReadsEveryNodeAndRecordsTheUnreachable(t *testing.T) {
	d, e := farm()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	targets := []Target{serveLedger(t, e), {Node: "gpuf", URL: "http://127.0.0.1:9"}, {Node: "gpug", URL: slow.URL}, serveLedger(t, d)}
	start := time.Now()
	nodes := Fetch(context.Background(), targets, 500*time.Millisecond)
	if time.Since(start) > 2*time.Second {
		t.Fatal("one slow node must not hold the fleet past the timeout")
	}
	if len(nodes) != 4 {
		t.Fatalf("%+v", nodes)
	}
	names := []string{}
	for _, n := range nodes {
		names = append(names, n.Name())
	}
	if strings.Join(names, " ") != "gpud gpue gpuf gpug" {
		t.Fatalf("sorted by the ledger's node name, the target's when unreachable: %v", names)
	}
	if nodes[0].Err != "" || len(nodes[0].Ledger.Entries) != 2 || !nodes[0].Ledger.NomadRead || nodes[0].Ledger.Entries[0].UUID != "GPU-d0" {
		t.Fatalf("round trip of /ledger: %+v", nodes[0])
	}
	if nodes[2].Err == "" || nodes[3].Err == "" {
		t.Fatalf("unreachable and timed-out nodes carry an error: %+v %+v", nodes[2], nodes[3])
	}
	// The URL is the target's already; the error says only what went wrong.
	if strings.Contains(nodes[2].Err, "Get ") || !strings.Contains(nodes[2].Err, "connection refused") {
		t.Fatalf("%q", nodes[2].Err)
	}
}

func TestSummarisePerNodeAndPerJob(t *testing.T) {
	d, e := farm()
	s := Summarise([]Node{{Ledger: d}, {Ledger: e}, {Target: Target{Node: "gpuf"}, Err: "connection refused"}})
	want := []NodeSummary{
		{Node: "gpud", GPUs: 2, Held: 1, ReservedIdle: 1, MemoryUsedMiB: 1024, MemoryTotalMiB: 16384},
		{Node: "gpue", GPUs: 2, Unaccounted: 1, Free: 1, MemoryUsedMiB: 2048, MemoryTotalMiB: 16384},
		{Node: "gpuf", Err: "connection refused"},
	}
	for i := range want {
		if s.Nodes[i] != want[i] {
			t.Errorf("node %d: got %+v, want %+v", i, s.Nodes[i], want[i])
		}
	}
	if s.Total != (NodeSummary{Node: "fleet", Schema: ledger.Schema, GPUs: 4, Held: 1, ReservedIdle: 1, Unaccounted: 1, Free: 1, MemoryUsedMiB: 3072, MemoryTotalMiB: 32768, Unreachable: 1}) {
		t.Errorf("total: %+v", s.Total)
	}
	if len(s.Jobs) != 2 || s.Jobs[0] != (JobSummary{Namespace: "default", Job: "restreamer", Reserved: 1, Held: 1}) || s.Jobs[1] != (JobSummary{Namespace: "video", Job: "worker", Reserved: 1, Held: 0}) {
		t.Errorf("jobs: %+v", s.Jobs)
	}
}

// A GPU with both a reserved tenant and an intruder is unaccounted, not held: held
// means every tenant is the one Nomad reserved the card for.
func TestAGPUWithAnyUnreservedTenantIsUnaccounted(t *testing.T) {
	l := ledger.Ledger{Node: "n", NomadRead: true, Entries: []ledger.Entry{{GPU: gpu(0, "GPU-a", 0, 1), Tenants: []ledger.Tenant{
		{Kind: ledger.KindNomad, AllocID: "a", Reserved: true, AllocVisible: true},
		{Kind: ledger.KindHost},
	}}}}
	if n := Summarise([]Node{{Ledger: l}}).Nodes[0]; n.Held != 0 || n.Unaccounted != 1 {
		t.Fatalf("%+v", n)
	}
}

func TestFindingsAcrossTheFleet(t *testing.T) {
	d, e := farm()
	fs := Findings([]Node{{Ledger: d}, {Ledger: e}, {Target: Target{Node: "gpuf"}, Err: "connection refused"}}, findings.Default)
	if len(fs) == 0 || fs[0].Level != findings.ERROR || fs[0].Code != "source-unavailable" || fs[0].Node != "gpuf" || !strings.Contains(fs[0].Message, "connection refused") {
		t.Fatalf("the unreachable node first: %+v", fs)
	}
	codes := map[string]string{}
	for _, f := range fs {
		codes[f.Node+" "+f.Code] = string(f.Level)
	}
	for k, v := range map[string]string{"gpud held": "OK", "gpud reserved-idle": "WARN", "gpue unmanaged-tenant": "BAD", "gpue idle": "OK"} {
		if codes[k] != v {
			t.Errorf("%s: got %q, want %s (%v)", k, codes[k], v, codes)
		}
	}
	if findings.Worst(fs) != findings.ERROR {
		t.Error("worst is ERROR")
	}
}

// A node on an older gpuledger shows its schema, so a mixed fleet is visible.
func TestSummaryCarriesEachNodesSchema(t *testing.T) {
	d, e := farm()
	d.Schema, e.Schema = 1, 0
	s := Summarise([]Node{{Ledger: d}, {Ledger: e}})
	if s.Nodes[0].Schema != 1 || s.Nodes[1].Schema != 0 || s.Schema != ledger.Schema {
		t.Fatalf("%+v", s)
	}
}
