package render

import (
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/findings"
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
