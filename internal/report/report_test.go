package report

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prom answers the report's two queries with canned vectors, as /api/v1/query does.
func prom(t *testing.T, seen *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		*seen = append(*seen, q+" auth="+r.Header.Get("Authorization"))
		switch {
		case strings.Contains(q, "gpuledger_job_gpu_seconds_total"):
			w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			 {"metric":{"namespace":"default","nomad_job":"restreamer","use":"held"},"value":[1,"36000"]},
			 {"metric":{"namespace":"default","nomad_job":"restreamer","use":"idle"},"value":[1,"0"]},
			 {"metric":{"namespace":"video","nomad_job":"worker","use":"held"},"value":[1,"7200"]},
			 {"metric":{"namespace":"video","nomad_job":"worker","use":"idle"},"value":[1,"28800"]}]}}`))
		case strings.Contains(q, "gpuledger_gpu_state_seconds_total"):
			w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			 {"metric":{"node":"gpud","state":"held"},"value":[1,"43200"]},
			 {"metric":{"node":"gpud","state":"free"},"value":[1,"14400"]},
			 {"metric":{"node":"gpue","state":"reserved-idle"},"value":[1,"28800.5"]}]}}`))
		default:
			w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"unexpected query"}`))
		}
	}))
}

func TestReportFromPrometheus(t *testing.T) {
	var seen []string
	srv := prom(t, &seen)
	defer srv.Close()
	t.Setenv("TEST_PROM_TOKEN", "p.secret")
	r, err := New(srv.URL, "TEST_PROM_TOKEN").Build(context.Background(), "7d")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range seen {
		if !strings.Contains(q, "[7d]") || !strings.Contains(q, "increase(") || !strings.HasSuffix(q, "auth=Bearer p.secret") {
			t.Fatalf("query: %s", q)
		}
	}
	// Worst waste first: worker left 8 of its 10 GPU-hours idle.
	if len(r.Jobs) != 2 || r.Jobs[0] != (Job{Namespace: "video", Job: "worker", HeldHours: 2, IdleHours: 8, ReservedHours: 10, IdleShare: 0.8}) || r.Jobs[1].Job != "restreamer" || r.Jobs[1].IdleShare != 0 {
		t.Fatalf("jobs: %+v", r.Jobs)
	}
	if len(r.Nodes) != 2 || r.Nodes[0].Node != "gpud" || r.Nodes[0].Hours["held"] != 12 || r.Nodes[0].Hours["free"] != 4 || r.Nodes[1].Hours["reserved-idle"] < 8 {
		t.Fatalf("nodes: %+v", r.Nodes)
	}
	if r.Schema != 1 || r.Since != "7d" {
		t.Fatalf("%+v", r)
	}
}

func TestReportErrors(t *testing.T) {
	if _, err := New("http://127.0.0.1:9", "").Build(context.Background(), "7d"); err == nil {
		t.Fatal("Prometheus down is an error: the report has nothing to say")
	}
	var seen []string
	srv := prom(t, &seen)
	defer srv.Close()
	for _, bad := range []string{"", "7", "7 days", "1d; drop", "-1h"} {
		if _, err := New(srv.URL, "").Build(context.Background(), bad); err == nil {
			t.Errorf("--since %q must be refused", bad)
		}
	}
	if len(seen) != 0 {
		t.Fatalf("a bad window never reaches Prometheus: %v", seen)
	}
}
