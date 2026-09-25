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

// Containers that hold a GPU without a process come from a map; the ledger must not
// inherit its random order, or the table and the metrics reshuffle on every refresh.
func TestTenantsWithoutProcessesHaveAStableOrder(t *testing.T) {
	in := Inputs{Node: "gpud", GPUs: []nvidia.GPU{{Index: 0, UUID: uA}}, Containers: map[string]containers.Container{}}
	for _, n := range []string{"e", "b", "d", "a", "c"} {
		id := repeat(n, 64)
		in.Containers[id] = containers.Container{ID: id, Name: "worker-" + n, GPUs: []string{uA}}
	}
	for i := 0; i < 20; i++ {
		got := ""
		for _, t := range Build(in).Entries[0].Tenants {
			got += t.Container + " "
		}
		if got != "worker-a worker-b worker-c worker-d worker-e " {
			t.Fatalf("run %d: %q", i, got)
		}
	}
}

// Nomad answers the node's allocations filtered by the token's namespaces, silently.
// A container whose allocation Nomad did not return must be told apart from one Nomad
// returned without this GPU — only the second is known to be unreserved.
func TestAllocVisibilityIsRecorded(t *testing.T) {
	in := inputs()
	if l := Build(in); l.NomadRead {
		t.Fatal("no Allocs → Nomad was not read")
	}
	in.Allocs = map[string]string{"77777777-0000-0000-0000-000000000000": "default"}
	in.Reservations = in.Reservations[1:]
	l := Build(in)
	if !l.NomadRead {
		t.Fatal("Allocs present → Nomad was read")
	}
	for _, tn := range l.Entries[0].Tenants {
		if tn.Kind == KindNomad && (tn.AllocVisible || tn.Reserved) {
			t.Fatalf("the restreamer's alloc was not returned: %+v", tn)
		}
	}
	in.Allocs["3e5d2f75-1111-2222-3333-444444444444"] = "default"
	for _, tn := range Build(in).Entries[0].Tenants {
		if tn.Kind == KindNomad && !tn.AllocVisible {
			t.Fatalf("returned alloc must be visible: %+v", tn)
		}
	}
}

func TestClassifyAndBuildSetTheState(t *testing.T) {
	nomadT := Tenant{Kind: KindNomad, AllocID: "a", Reserved: true, AllocVisible: true}
	res := []nomad.Reservation{{AllocID: "a"}}
	for _, c := range []struct {
		e    Entry
		want State
	}{
		{Entry{}, StateFree},
		{Entry{Reservations: res}, StateReservedIdle},
		{Entry{Reservations: res, Tenants: []Tenant{nomadT}}, StateHeld},
		{Entry{Reservations: res, Tenants: []Tenant{nomadT, {Kind: KindHost}}}, StateUnaccounted},
		{Entry{Tenants: []Tenant{{Kind: KindDocker}}}, StateUnaccounted},
	} {
		if got := Classify(c.e); got != c.want {
			t.Errorf("%+v: got %s, want %s", c.e, got, c.want)
		}
	}
	l := Build(inputs())
	if l.Entries[0].State != StateUnaccounted || l.Entries[1].State != StateReservedIdle {
		t.Fatalf("Build sets State: %s %s", l.Entries[0].State, l.Entries[1].State)
	}
}

// An allocation read from a container's name is trusted only when Nomad returns it: a
// container someone named like a Nomad task is not Nomad's.
func TestAllocFromNameNeedsNomadToKnowIt(t *testing.T) {
	cid := repeat("d", 64)
	in := Inputs{Node: "n", GPUs: []nvidia.GPU{{Index: 0, UUID: uA}}, Processes: []nvidia.Process{{GPUUUID: uA, PID: 1}},
		ContainerOf:  func(int) string { return cid },
		Containers:   map[string]containers.Container{cid: {ID: cid, Name: "enc-a1", AllocID: "a1", AllocFromName: true, TaskName: "enc"}},
		Allocs:       map[string]string{"a1": "default"},
		Reservations: []nomad.Reservation{{AllocID: "a1", DeviceIDs: []string{uA}}}}
	if tn := Build(in).Entries[0].Tenants[0]; tn.Kind != KindNomad || !tn.Reserved || !tn.AllocFromName {
		t.Fatalf("returned by Nomad: %+v", tn)
	}
	in.Allocs, in.Reservations = map[string]string{}, nil
	if tn := Build(in).Entries[0].Tenants[0]; tn.Kind != KindNomad || tn.AllocVisible || !tn.AllocFromName {
		t.Fatalf("not returned: still a Nomad tenant Nomad cannot vouch for, flagged as named: %+v", tn)
	}
}

func TestBuildStampsTheSchema(t *testing.T) {
	if l := Build(inputs()); l.Schema != Schema || Schema != 1 {
		t.Fatalf("schema %d, want %d", l.Schema, Schema)
	}
}
