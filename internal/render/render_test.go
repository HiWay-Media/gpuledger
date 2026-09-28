package render

import (
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/fleet"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nvidia"
	"github.com/hiway-media/gpuledger/internal/report"
)

func TestLedgerTableAndFindingsText(t *testing.T) {
	l := ledger.Ledger{Node: "gpud", At: time.Unix(0, 0), Errors: []string{"x"}, Entries: []ledger.Entry{{GPU: nvidia.GPU{Index: 0, Model: "L4", MemoryUsedMiB: 1, MemoryTotalMiB: 2}, Tenants: []ledger.Tenant{{Kind: ledger.KindHost, Processes: []string{"ffmpeg"}, UsedMemoryMiB: 1}}}}}
	out := Ledger(l)
	if !strings.Contains(out, "1 source error(s)") || !strings.Contains(out, "host:ffmpeg (1 MiB) !unmanaged") || !strings.Contains(out, "│ gpu │ model") {
		t.Fatalf("%s", out)
	}
	f := Findings([]findings.Finding{{Level: findings.BAD, Code: "unmanaged-tenant", Node: "gpud", GPU: "gpu0 GPU-aaaa", Message: "m"}, {Level: findings.OK, Code: "held", Node: "gpud", Message: "ok"}})
	if !strings.Contains(f, "🔴 BAD") || !strings.Contains(f, "2 findings: 1 OK, 0 WARN, 1 BAD, 0 ERROR") {
		t.Fatalf("%s", f)
	}
}

func TestFleetTables(t *testing.T) {
	s := fleet.Summary{
		Nodes: []fleet.NodeSummary{{Node: "gpud", Schema: 1, GPUs: 2, Held: 1, ReservedIdle: 1, MemoryUsedMiB: 1024, MemoryTotalMiB: 16384}, {Node: "gpuf", Err: "connection refused"}},
		Jobs:  []fleet.JobSummary{{Namespace: "video", Job: "worker", Reserved: 2, Held: 1}},
		Total: fleet.NodeSummary{Node: "fleet", Schema: 1, GPUs: 2, Held: 1, ReservedIdle: 1, MemoryUsedMiB: 1024, MemoryTotalMiB: 16384, Unreachable: 1},
	}
	out := Fleet(s)
	for _, want := range []string{
		"gpuledger fleet · 2 node(s), 1 unreachable · 2 GPU(s)",
		"│ node  │ gpus │ held │ reserved-idle │ unaccounted │ free │ memory",
		"│ gpud  │ 2    │ 1    │ 1             │ 0           │ 0    │ 1024/16384 MiB",
		"│ gpuf  │ —    │ —    │ —             │ —           │ —    │ unreachable: connection refused",
		"│ fleet │ 2    │ 1    │ 1             │ 0           │ 0    │ 1024/16384 MiB",
		"│ video/worker │ 2        │ 1    │",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	f := FleetFindings([]findings.Finding{{Level: findings.ERROR, Code: "source-unavailable", Node: "gpuf", Message: "down"}, {Level: findings.WARN, Code: "reserved-idle", Node: "gpud", GPU: "gpu1 GPU-d1", Message: "m"}})
	if !strings.Contains(f, "gpuf ") || !strings.Contains(f, "gpud   gpu1 GPU-d1") || !strings.Contains(f, "2 findings: 0 OK, 1 WARN, 0 BAD, 1 ERROR") {
		t.Fatalf("%s", f)
	}
}

func TestLedgerTableShowsTheStateAndItsAge(t *testing.T) {
	at := time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC)
	since := at.Add(-90 * time.Minute)
	out := Ledger(ledger.Ledger{Node: "gpud", At: at, Entries: []ledger.Entry{{GPU: nvidia.GPU{Index: 0, Model: "L4"}, State: ledger.StateReservedIdle, StateSince: &since}, {GPU: nvidia.GPU{Index: 1, Model: "L4"}, State: ledger.StateFree}}})
	if !strings.Contains(out, "│ state ") || !strings.Contains(out, "reserved-idle 1h30m") || !strings.Contains(out, "│ free ") {
		t.Fatalf("%s", out)
	}
}

func TestFleetTableMarksANodeOnAnotherSchema(t *testing.T) {
	out := Fleet(fleet.Summary{Nodes: []fleet.NodeSummary{{Node: "gpua", Schema: 0, GPUs: 2}}, Total: fleet.NodeSummary{Node: "fleet", Schema: 1}})
	if !strings.Contains(out, "gpua (schema 0)") || strings.Contains(out, "fleet (schema") {
		t.Fatalf("%s", out)
	}
}

func TestReportTables(t *testing.T) {
	out := Report(report.Report{Since: "7d", Jobs: []report.Job{
		{Namespace: "video", Job: "worker", ReservedHours: 10, HeldHours: 2, IdleHours: 8, IdleShare: 0.8},
		{Namespace: "default", Job: "restreamer", ReservedHours: 10, HeldHours: 10},
	}, Nodes: []report.Node{{Node: "gpud", Hours: map[string]float64{"held": 12, "free": 4.25}}}})
	for _, want := range []string{
		"gpuledger report · the last 7d · 2 job(s), 20.0 GPU-hours reserved, 8.0 idle (40%)",
		"│ job                │ reserved GPU-h │ held GPU-h │ idle GPU-h │ idle │",
		"│ video/worker       │ 10.0           │ 2.0        │ 8.0        │ 80%  │",
		"│ node │ held │ reserved-idle │ unaccounted │ free │",
		"│ gpud │ 12.0 │ 0.0           │ 0.0         │ 4.2  │",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// A Prometheus that has not scraped the counters twice in the window has nothing to
// report: say so, rather than print empty tables.
func TestEmptyReportSaysWhy(t *testing.T) {
	out := Report(report.Report{Since: "7d"})
	if !strings.Contains(out, "no gpuledger counters in Prometheus over the last 7d") {
		t.Fatalf("%s", out)
	}
}
