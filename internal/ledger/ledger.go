// Package ledger joins the three sources into one answer per GPU: what the driver sees
// on it, who Nomad reserved it for, and which containers or host processes actually
// hold it. Pure: every input is data, so the join is unit-tested without a GPU.
package ledger

import (
	"sort"
	"strings"
	"time"

	"github.com/hiway-media/gpuledger/internal/containers"
	"github.com/hiway-media/gpuledger/internal/nomad"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

// Kind says how a tenant got onto the GPU.
type Kind string

const (
	KindNomad  Kind = "nomad"  // a container Nomad's docker driver started
	KindDocker Kind = "docker" // a container nobody orchestrates
	KindHost   Kind = "host"   // a bare process on the node
)

// Tenant is one holder of a GPU: a container (with or without Nomad behind it) or a
// host process. Memory is the sum of its processes' memory on that GPU.
type Tenant struct {
	Kind          Kind     `json:"kind"`
	Container     string   `json:"container,omitempty"`
	ContainerID   string   `json:"containerId,omitempty"`
	Image         string   `json:"image,omitempty"`
	AllocID       string   `json:"allocId,omitempty"`
	JobName       string   `json:"jobName,omitempty"`
	TaskName      string   `json:"taskName,omitempty"`
	Namespace     string   `json:"namespace,omitempty"`
	PIDs          []int    `json:"pids"`
	Processes     []string `json:"processes"`
	UsedMemoryMiB int      `json:"usedMemoryMiB"`
	// Reserved is true when Nomad allocated this GPU to this tenant's allocation.
	Reserved bool `json:"reserved"`
	// AllocVisible is true when Nomad returned the tenant's allocation at all. Nomad
	// filters the node's allocations by the token's namespaces without an error, so a
	// Nomad tenant that is not reserved is only known to be unreserved when this is true.
	AllocVisible bool `json:"allocVisible"`
}

// Entry is one GPU's row in the ledger.
type Entry struct {
	nvidia.GPU
	Reservations []nomad.Reservation `json:"reservations"`
	Tenants      []Tenant            `json:"tenants"`
}

// Ledger is one node's snapshot.
type Ledger struct {
	Node    string    `json:"node"`
	At      time.Time `json:"at"`
	Entries []Entry   `json:"entries"`
	// NomadRead is true when the node's allocations were read, so reservations mean
	// something; false with --no-nomad or when Nomad could not be asked.
	NomadRead bool `json:"nomadRead"`
	// Errors are the sources that could not be read; each is also a finding.
	Errors []string `json:"errors,omitempty"`
}

// Inputs are the raw reads; any of them may be nil when its source failed.
type Inputs struct {
	Node         string
	At           time.Time
	GPUs         []nvidia.GPU
	Processes    []nvidia.Process
	ContainerOf  func(pid int) string // pid → container id, "" for a host process
	Containers   map[string]containers.Container
	Reservations []nomad.Reservation
	// Allocs is every allocation Nomad returned for the node, id → namespace; nil when
	// Nomad was not read.
	Allocs map[string]string
	Errors []string
}

func gpuMatches(ids []string, g nvidia.GPU) bool {
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "all" || id == g.UUID || id == strings.TrimPrefix(g.UUID, "GPU-") || id == itoa(g.Index) {
			return true
		}
		// The device plugin may report a short form "GPU-fef8089b".
		if strings.HasPrefix(g.UUID, id) && len(id) >= 8 {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

// Build joins the inputs.
func Build(in Inputs) Ledger {
	l := Ledger{Node: in.Node, At: in.At, Errors: in.Errors, NomadRead: in.Allocs != nil}
	for _, g := range in.GPUs {
		e := Entry{GPU: g}
		reservedAllocs := map[string]bool{}
		for _, r := range in.Reservations {
			if gpuMatches(r.DeviceIDs, g) {
				e.Reservations = append(e.Reservations, r)
				reservedAllocs[r.AllocID] = true
			}
		}
		// Tenants from processes the driver sees.
		byKey := map[string]*Tenant{}
		order := []string{}
		add := func(key string, t Tenant) *Tenant {
			if existing, ok := byKey[key]; ok {
				return existing
			}
			byKey[key] = &t
			order = append(order, key)
			return byKey[key]
		}
		for _, p := range in.Processes {
			if p.GPUUUID != g.UUID {
				continue
			}
			cid := ""
			if in.ContainerOf != nil {
				cid = in.ContainerOf(p.PID)
			}
			var t *Tenant
			if cid == "" {
				t = add("host", Tenant{Kind: KindHost})
			} else if c, ok := in.Containers[cid]; ok {
				kind := KindDocker
				if c.NomadManaged() {
					kind = KindNomad
				}
				t = add("c:"+cid, Tenant{Kind: kind, Container: c.Name, ContainerID: short(cid), Image: c.Image, AllocID: c.AllocID, JobName: c.JobName, TaskName: c.TaskName, Namespace: c.Namespace, Reserved: reservedAllocs[c.AllocID], AllocVisible: visible(in.Allocs, c.AllocID)})
			} else {
				t = add("c:"+cid, Tenant{Kind: KindDocker, ContainerID: short(cid)})
			}
			t.PIDs = append(t.PIDs, p.PID)
			t.Processes = append(t.Processes, p.Name)
			t.UsedMemoryMiB += p.UsedMemoryMiB
		}
		// Containers that hold the device without a process right now (a worker
		// between jobs) are tenants too, with no PIDs.
		for cid, c := range in.Containers {
			if _, seen := byKey["c:"+cid]; seen || len(c.GPUs) == 0 || !gpuMatches(c.GPUs, g) {
				continue
			}
			kind := KindDocker
			if c.NomadManaged() {
				kind = KindNomad
			}
			add("c:"+cid, Tenant{Kind: kind, Container: c.Name, ContainerID: short(cid), Image: c.Image, AllocID: c.AllocID, JobName: c.JobName, TaskName: c.TaskName, Namespace: c.Namespace, Reserved: reservedAllocs[c.AllocID], AllocVisible: visible(in.Allocs, c.AllocID), PIDs: []int{}, Processes: []string{}})
		}
		for _, k := range order {
			e.Tenants = append(e.Tenants, *byKey[k])
		}
		// Most memory first; ties by name then id, so the order never depends on a map.
		sort.SliceStable(e.Tenants, func(i, j int) bool {
			a, b := e.Tenants[i], e.Tenants[j]
			if a.UsedMemoryMiB != b.UsedMemoryMiB {
				return a.UsedMemoryMiB > b.UsedMemoryMiB
			}
			if a.Container != b.Container {
				return a.Container < b.Container
			}
			return a.ContainerID < b.ContainerID
		})
		l.Entries = append(l.Entries, e)
	}
	sort.Slice(l.Entries, func(i, j int) bool { return l.Entries[i].Index < l.Entries[j].Index })
	return l
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func visible(allocs map[string]string, id string) bool {
	_, ok := allocs[id]
	return id != "" && ok
}
