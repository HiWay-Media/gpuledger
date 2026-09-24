// Package findings turns a ledger into verdicts. Worst first; exit code 0 unless asked
// otherwise, because a check that ran is a success — the same contract as checkfleet.
package findings

import (
	"fmt"
	"sort"

	"github.com/hiway-media/gpuledger/internal/ledger"
)

type Level string

const (
	OK    Level = "OK"
	WARN  Level = "WARN"
	BAD   Level = "BAD"
	ERROR Level = "ERROR"
)

var rank = map[Level]int{OK: 0, WARN: 1, BAD: 2, ERROR: 3}

// Finding is one verdict about one GPU (or the node, when GPU is empty).
type Finding struct {
	Level   Level  `json:"level"`
	Code    string `json:"code"`
	Node    string `json:"node"`
	GPU     string `json:"gpu,omitempty"` // "index uuid-prefix"
	Message string `json:"message"`
}

// Policy holds the thresholds; every one has a defensible default and a flag.
type Policy struct {
	EncoderMax     int  // encoder sessions at or above which the GPU counts as saturated
	TempMaxC       int  // temperature at or above which the GPU counts as hot
	AllowUnmanaged bool // demote unmanaged tenants from BAD to WARN
	IdleIsFinding  bool // report a GPU with no tenant and no reservation (capacity)
}

// Default is what ships: NVENC session limits vary by card, 8 is conservative for
// datacenter parts; 85 °C is where consumer and Quadro parts start throttling.
var Default = Policy{EncoderMax: 8, TempMaxC: 85, AllowUnmanaged: false, IdleIsFinding: true}

func gpuLabel(e ledger.Entry) string {
	u := e.UUID
	if len(u) > 12 {
		u = u[:12]
	}
	return fmt.Sprintf("gpu%d %s", e.Index, u)
}

// Evaluate returns the findings, worst first. Codes:
//
//	source-unavailable  a collector failed (ERROR)
//	unreserved-tenant   a Nomad container holds a GPU Nomad did not give it (BAD)
//	unmanaged-tenant    a container nobody orchestrates, or a host process (BAD, WARN with AllowUnmanaged)
//	contended           two or more distinct tenants on one GPU (WARN)
//	reserved-idle       Nomad reserved the GPU, nothing runs on it (WARN)
//	encoder-saturated   encoder sessions ≥ EncoderMax (WARN)
//	hot                 temperature ≥ TempMaxC (WARN)
//	idle                no tenant, no reservation: free capacity (OK, informational)
//	held                the GPU has tenants and every one is reserved (OK)
func Evaluate(l ledger.Ledger, p Policy) []Finding {
	var out []Finding
	for _, err := range l.Errors {
		out = append(out, Finding{Level: ERROR, Code: "source-unavailable", Node: l.Node, Message: err})
	}
	for _, e := range l.Entries {
		g := gpuLabel(e)
		if len(e.Tenants) == 0 {
			if len(e.Reservations) > 0 {
				r := e.Reservations[0]
				out = append(out, Finding{Level: WARN, Code: "reserved-idle", Node: l.Node, GPU: g, Message: fmt.Sprintf("reserved by %s/%s (alloc %s) but no process or container holds it", r.JobID, r.Task, short(r.AllocID))})
			} else if p.IdleIsFinding {
				out = append(out, Finding{Level: OK, Code: "idle", Node: l.Node, GPU: g, Message: fmt.Sprintf("%s idle: no tenant, no reservation, %d MiB free", e.Model, e.MemoryTotalMiB-e.MemoryUsedMiB)})
			}
		}
		allReserved := len(e.Tenants) > 0
		for _, t := range e.Tenants {
			switch {
			case t.Kind == ledger.KindNomad && !t.Reserved:
				allReserved = false
				out = append(out, Finding{Level: BAD, Code: "unreserved-tenant", Node: l.Node, GPU: g, Message: fmt.Sprintf("Nomad task %s/%s (alloc %s, container %s) uses this GPU but was not allocated it — check NVIDIA_VISIBLE_DEVICES and the job's device stanza", t.JobName, t.TaskName, short(t.AllocID), t.Container)})
			case t.Kind == ledger.KindDocker:
				allReserved = false
				lvl := BAD
				if p.AllowUnmanaged {
					lvl = WARN
				}
				out = append(out, Finding{Level: lvl, Code: "unmanaged-tenant", Node: l.Node, GPU: g, Message: fmt.Sprintf("container %s (%s) holds this GPU outside Nomad — %d MiB, pids %v", t.Container, t.Image, t.UsedMemoryMiB, t.PIDs)})
			case t.Kind == ledger.KindHost:
				allReserved = false
				lvl := BAD
				if p.AllowUnmanaged {
					lvl = WARN
				}
				out = append(out, Finding{Level: lvl, Code: "unmanaged-tenant", Node: l.Node, GPU: g, Message: fmt.Sprintf("host process %v holds this GPU outside any container — %d MiB, pids %v", t.Processes, t.UsedMemoryMiB, t.PIDs)})
			}
		}
		if len(e.Tenants) > 1 {
			names := []string{}
			for _, t := range e.Tenants {
				n := t.Container
				if n == "" {
					n = string(t.Kind)
				}
				names = append(names, n)
			}
			out = append(out, Finding{Level: WARN, Code: "contended", Node: l.Node, GPU: g, Message: fmt.Sprintf("%d tenants share this GPU: %v", len(e.Tenants), names)})
		}
		if p.EncoderMax > 0 && e.EncoderSessions >= p.EncoderMax {
			out = append(out, Finding{Level: WARN, Code: "encoder-saturated", Node: l.Node, GPU: g, Message: fmt.Sprintf("%d encoder sessions (limit %d), %.0f fps average", e.EncoderSessions, p.EncoderMax, e.EncoderFPS)})
		}
		if p.TempMaxC > 0 && e.TemperatureC >= p.TempMaxC {
			out = append(out, Finding{Level: WARN, Code: "hot", Node: l.Node, GPU: g, Message: fmt.Sprintf("%d °C (limit %d), %.0f W", e.TemperatureC, p.TempMaxC, e.PowerW)})
		}
		if allReserved {
			out = append(out, Finding{Level: OK, Code: "held", Node: l.Node, GPU: g, Message: fmt.Sprintf("%d tenant(s), all reserved by Nomad, %d%% util", len(e.Tenants), e.UtilizationPct)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Level] > rank[out[j].Level] })
	return out
}

// Worst returns the highest level present, OK for none.
func Worst(fs []Finding) Level {
	w := OK
	for _, f := range fs {
		if rank[f.Level] > rank[w] {
			w = f.Level
		}
	}
	return w
}

// ExitCode maps the worst level to a process exit code under an --exit-on policy:
// "" → always 0; "warn" → 1 at WARN, 2 at BAD, 3 at ERROR; "bad" → 0 below BAD; "error".
func ExitCode(fs []Finding, exitOn string) int {
	w := Worst(fs)
	code := map[Level]int{OK: 0, WARN: 1, BAD: 2, ERROR: 3}[w]
	switch exitOn {
	case "warn":
		return code
	case "bad":
		if w == WARN {
			return 0
		}
		return code
	case "error":
		if w == ERROR {
			return 3
		}
		return 0
	}
	return 0
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
