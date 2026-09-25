package metrics

import (
	"strings"
	"testing"
	"time"

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
		`gpuledger_tenant_memory_bytes{node="gpud",gpu="0",uuid="GPU-aaaa",kind="docker",container="gpu-d-c0",container_id="",job="",task="",alloc="",namespace=""} 1048576`,
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

// Two containers the Docker API could not name must still be two series: a duplicate
// series makes Prometheus reject the scrape.
func TestTenantSeriesAreUniqueEvenForUnnamedContainers(t *testing.T) {
	l := ledger.Ledger{Node: "gpud", Entries: []ledger.Entry{{GPU: nvidia.GPU{Index: 0, UUID: "GPU-aaaa"}, Tenants: []ledger.Tenant{
		{Kind: ledger.KindDocker, ContainerID: "aaaaaaaaaaaa"},
		{Kind: ledger.KindDocker, ContainerID: "bbbbbbbbbbbb"},
	}}}}
	seen := map[string]bool{}
	for _, line := range strings.Split(Render(l), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		series := line[:strings.LastIndex(line, " ")]
		if seen[series] {
			t.Fatalf("duplicate series %s", series)
		}
		seen[series] = true
	}
	if !seen[`gpuledger_tenant_reserved{node="gpud",gpu="0",uuid="GPU-aaaa",kind="docker",container="",container_id="bbbbbbbbbbbb",job="",task="",alloc="",namespace=""}`] {
		t.Fatalf("container_id label missing:\n%s", Render(l))
	}
}

// Each family's HELP, TYPE and samples must sit together, TYPE before the first sample:
// the exposition format (and OpenMetrics parsers) reject a family split by another.
func TestFamiliesAreContiguous(t *testing.T) {
	g := func(i int, u string) ledger.Entry {
		return ledger.Entry{GPU: nvidia.GPU{Index: i, UUID: u}, Tenants: []ledger.Tenant{{Kind: ledger.KindHost}}}
	}
	out := Render(ledger.Ledger{Node: "gpud", Entries: []ledger.Entry{g(0, "GPU-a"), g(1, "GPU-b")}})
	done := map[string]bool{}
	current := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name := ""
		switch {
		case strings.HasPrefix(line, "# HELP "), strings.HasPrefix(line, "# TYPE "):
			name = strings.Fields(line)[2]
		default:
			name = line[:strings.IndexAny(line, "{ ")]
		}
		if name != current {
			if done[name] {
				t.Fatalf("family %s split:\n%s", name, out)
			}
			if current != "" {
				done[current] = true
			}
			if !strings.HasPrefix(line, "# HELP ") {
				t.Fatalf("family %s starts without HELP: %q", name, line)
			}
			current = name
		}
	}
}

func TestStateAndSinceGauges(t *testing.T) {
	since := time.Unix(1790000000, 0)
	out := Render(ledger.Ledger{Node: "gpud", Entries: []ledger.Entry{
		{GPU: nvidia.GPU{Index: 0, UUID: "GPU-a"}, State: ledger.StateReservedIdle, StateSince: &since},
		{GPU: nvidia.GPU{Index: 1, UUID: "GPU-b"}, State: ledger.StateFree},
	}})
	for _, want := range []string{
		`gpuledger_gpu_state{node="gpud",gpu="0",uuid="GPU-a",state="reserved-idle"} 1`,
		`gpuledger_gpu_state{node="gpud",gpu="1",uuid="GPU-b",state="free"} 1`,
		`gpuledger_gpu_state_since_timestamp_seconds{node="gpud",gpu="0",uuid="GPU-a",state="reserved-idle"} 1790000000`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, `gpuledger_gpu_state_since_timestamp_seconds{node="gpud",gpu="1"`) {
		t.Error("no since without history")
	}
}
