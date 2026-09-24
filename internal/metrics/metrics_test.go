package metrics

import (
	"strings"
	"testing"

	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

func TestRenderIsPrometheusTextAndLeaksNothing(t *testing.T) {
	l := ledger.Ledger{Node: "gpud", Entries: []ledger.Entry{{GPU: nvidia.GPU{Index: 0, UUID: "GPU-aaaa", Model: `Quadro "RTX" 4000`, MemoryUsedMiB: 1, MemoryTotalMiB: 2, TemperatureC: 3, PowerW: 4.5, EncoderSessions: 6}, Tenants: []ledger.Tenant{{Kind: ledger.KindDocker, Container: "gpu-d-c0", Image: "img", UsedMemoryMiB: 1, PIDs: []int{4242}, Processes: []string{"HandBrakeCLI"}}}}}}
	out := Render(l)
	for _, want := range []string{
		`gpuledger_up{node="gpud"} 1`,
		`gpuledger_gpu_info{node="gpud",gpu="0",uuid="GPU-aaaa",model="Quadro \"RTX\" 4000",bus=""} 1`,
		`gpuledger_gpu_memory_used_bytes{node="gpud",gpu="0",uuid="GPU-aaaa"} 1048576`,
		`gpuledger_gpu_power_watts{node="gpud",gpu="0",uuid="GPU-aaaa"} 4.5`,
		`gpuledger_gpu_tenants{node="gpud",gpu="0",uuid="GPU-aaaa"} 1`,
		`gpuledger_tenant_memory_bytes{node="gpud",gpu="0",uuid="GPU-aaaa",kind="docker",container="gpu-d-c0",job="",task="",alloc="",namespace=""} 1048576`,
		`gpuledger_tenant_reserved{`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "HandBrakeCLI") || strings.Contains(out, "4242") || strings.Contains(out, "img") {
		t.Fatal("process names, pids and images must not be metric labels")
	}
	l.Errors = []string{"nomad: down"}
	if !strings.Contains(Render(l), `gpuledger_up{node="gpud"} 0`) {
		t.Fatal("up must be 0 with a source error")
	}
}
