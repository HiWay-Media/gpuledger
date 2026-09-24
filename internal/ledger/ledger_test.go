package ledger

import (
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/containers"
	"github.com/hiway-media/gpuledger/internal/nomad"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

const uA = "GPU-fef8089b-4a2c-4d1e-9d53-1f2b3c4d5e6f"
const uB = "GPU-ac81e44d-1234-4d1e-9d53-abcdefabcdef"

var cidA = repeat("a", 64)
var cidB = repeat("b", 64)

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func inputs() Inputs {
	return Inputs{
		Node: "gpud", At: time.Unix(0, 0),
		GPUs:      []nvidia.GPU{{Index: 0, UUID: uA, Model: "Quadro RTX 4000", MemoryTotalMiB: 8192}, {Index: 1, UUID: uB, Model: "Quadro RTX 4000", MemoryTotalMiB: 8192}},
		Processes: []nvidia.Process{{GPUUUID: uA, PID: 4242, UsedMemoryMiB: 2048, Name: "HandBrakeCLI"}, {GPUUUID: uA, PID: 5151, UsedMemoryMiB: 1024, Name: "ffmpeg"}, {GPUUUID: uA, PID: 6060, UsedMemoryMiB: 48, Name: "python3"}},
		ContainerOf: func(pid int) string {
			return map[int]string{4242: cidA, 5151: cidB}[pid]
		},
		Containers: map[string]containers.Container{
			cidA: {ID: cidA, Name: "gpu-d-new-c0", Image: "encoding:handbrake-4.1.3-prod", GPUs: []string{uA}},
			cidB: {ID: cidB, Name: "restreamer-3e5d2f75-abcd", Image: "restreamer:4.1.2", AllocID: "3e5d2f75-1111-2222-3333-444444444444", JobName: "gpu-gpud-restreamer", TaskName: "restreamer", Namespace: "default", GPUs: []string{"GPU-fef8089b"}},
		},
		Reservations: []nomad.Reservation{{AllocID: "3e5d2f75-1111-2222-3333-444444444444", JobID: "gpu-gpud-restreamer", Task: "restreamer", Status: "running", DeviceIDs: []string{uA}}, {AllocID: "77777777-0000-0000-0000-000000000000", JobID: "tngrm-video-worker-gpud", Task: "worker", Status: "running", DeviceIDs: []string{uB}}},
	}
}

func TestBuildJoinsTenantsAndReservations(t *testing.T) {
	l := Build(inputs())
	if len(l.Entries) != 2 {
		t.Fatalf("entries: %d", len(l.Entries))
	}
	a := l.Entries[0]
	if len(a.Reservations) != 1 || a.Reservations[0].JobID != "gpu-gpud-restreamer" {
		t.Fatalf("reservations on gpu0: %+v", a.Reservations)
	}
	if len(a.Tenants) != 3 {
		t.Fatalf("tenants on gpu0: %+v", a.Tenants)
	}
	// sorted by memory: the unmanaged encoding container first
	if a.Tenants[0].Kind != KindDocker || a.Tenants[0].Container != "gpu-d-new-c0" || a.Tenants[0].UsedMemoryMiB != 2048 || a.Tenants[0].Reserved {
		t.Fatalf("tenant 0: %+v", a.Tenants[0])
	}
	if a.Tenants[1].Kind != KindNomad || a.Tenants[1].JobName != "gpu-gpud-restreamer" || !a.Tenants[1].Reserved {
		t.Fatalf("tenant 1: %+v", a.Tenants[1])
	}
	if a.Tenants[2].Kind != KindHost || a.Tenants[2].Processes[0] != "python3" || a.Tenants[2].PIDs[0] != 6060 {
		t.Fatalf("tenant 2: %+v", a.Tenants[2])
	}
	b := l.Entries[1]
	if len(b.Tenants) != 0 || len(b.Reservations) != 1 || b.Reservations[0].JobID != "tngrm-video-worker-gpud" {
		t.Fatalf("gpu1 must be reserved and idle: %+v", b)
	}
}

func TestContainerHoldingAGPUWithoutAProcessIsATenant(t *testing.T) {
	in := inputs()
	in.Processes = nil
	in.Containers[cidB] = containers.Container{ID: cidB, Name: "worker-idle", AllocID: "77777777-0000-0000-0000-000000000000", JobName: "tngrm-video-worker-gpud", TaskName: "worker", GPUs: []string{uB}}
	l := Build(in)
	if len(l.Entries[1].Tenants) != 1 || len(l.Entries[1].Tenants[0].PIDs) != 0 || !l.Entries[1].Tenants[0].Reserved {
		t.Fatalf("gpu1: %+v", l.Entries[1].Tenants)
	}
	// gpu0: the encoding container declares uA and is still a (docker) tenant with no pids
	if len(l.Entries[0].Tenants) != 1 || l.Entries[0].Tenants[0].Kind != KindDocker {
		t.Fatalf("gpu0: %+v", l.Entries[0].Tenants)
	}
}

func TestDeviceIdForms(t *testing.T) {
	g := nvidia.GPU{Index: 1, UUID: uB}
	for _, ids := range [][]string{{uB}, {"GPU-ac81e44d"}, {"1"}, {"all"}, {"x", uB}} {
		if !gpuMatches(ids, g) {
			t.Fatalf("%v should match", ids)
		}
	}
	for _, ids := range [][]string{{uA}, {"0"}, {"GPU-"}, {}} {
		if gpuMatches(ids, g) {
			t.Fatalf("%v should not match", ids)
		}
	}
}

func TestAnUnknownContainerIdStillCountsAsDocker(t *testing.T) {
	in := inputs()
	delete(in.Containers, cidA)
	l := Build(in)
	if l.Entries[0].Tenants[0].Kind != KindDocker || l.Entries[0].Tenants[0].ContainerID != cidA[:12] {
		t.Fatalf("%+v", l.Entries[0].Tenants[0])
	}
}
