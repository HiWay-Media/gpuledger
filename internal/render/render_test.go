package render

import (
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/fleet"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nvidia"
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
		Nodes: []fleet.NodeSummary{{Node: "gpud", GPUs: 2, Held: 1, ReservedIdle: 1, MemoryUsedMiB: 1024, MemoryTotalMiB: 16384}, {Node: "gpuf", Err: "connection refused"}},
		Jobs:  []fleet.JobSummary{{Namespace: "video", Job: "worker", Reserved: 2, Held: 1}},
		Total: fleet.NodeSummary{Node: "fleet", GPUs: 2, Held: 1, ReservedIdle: 1, MemoryUsedMiB: 1024, MemoryTotalMiB: 16384, Unreachable: 1},
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
