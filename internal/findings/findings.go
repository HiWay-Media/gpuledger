// Package findings turns a ledger into verdicts. Worst first; exit code 0 unless asked
// otherwise, because a check that ran is a success — the same contract as checkfleet.
package findings

import (
	"fmt"
	"sort"
	"time"

	"github.com/hiway-media/gpuledger/internal/cards"
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
	EncoderMax     int  // encoder sessions at or above which the GPU counts as saturated; 0: the card's own cap, <0: off
	TempMaxC       int  // temperature at or above which the GPU counts as hot
	AllowUnmanaged bool // demote unmanaged tenants from BAD to WARN
	IdleIsFinding  bool // report a GPU with no tenant and no reservation (capacity)
}

// Default is what ships. EncoderMax 0 takes each card's cap from NVIDIA's support
// matrix (internal/cards): none on Quadro, L4, T4, A10 — "Unrestricted" — and the
// driver's cap on GeForce. TempMaxC applies only where the driver reports neither a
// thermal margin nor a slowdown state; 85 °C is where Quadro parts start throttling.
var Default = Policy{EncoderMax: 0, TempMaxC: 85, AllowUnmanaged: false, IdleIsFinding: true}

// ThermalMarginMinC is the margin to the card's own slowdown temperature, as the driver
// reports it, at or below which the card counts as hot.
const ThermalMarginMinC = 5

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
				out = append(out, Finding{Level: WARN, Code: "reserved-idle", Node: l.Node, GPU: g, Message: fmt.Sprintf("reserved by %s/%s (alloc %s) but no process or container holds it%s", r.JobID, r.Task, short(r.AllocID), forHow(l, e))})
			} else if p.IdleIsFinding {
				out = append(out, Finding{Level: OK, Code: "idle", Node: l.Node, GPU: g, Message: fmt.Sprintf("%s idle: no tenant, no reservation, %d MiB free%s", e.Model, e.MemoryTotalMiB-e.MemoryUsedMiB, forHow(l, e))})
			}
		}
		allReserved := len(e.Tenants) > 0
		for _, t := range e.Tenants {
			switch {
			case t.Kind == ledger.KindNomad && !t.Reserved && !l.NomadRead:
				// No reservations to compare with: the source error, or --no-nomad, says so.
				allReserved = false
			case t.Kind == ledger.KindNomad && !t.Reserved && !t.AllocVisible:
				allReserved = false
				ns := t.Namespace
				if ns == "" {
					ns = "its namespace"
				} else {
					ns = fmt.Sprintf("%q", ns)
				}
				from := ""
				if t.AllocFromName {
					from = ", the allocation id read from the container's name"
				}
				out = append(out, Finding{Level: ERROR, Code: "source-unavailable", Node: l.Node, GPU: g, Message: fmt.Sprintf("Nomad did not return allocation %s of task %s/%s (container %s%s): the token needs read-job on namespace %s — whether this GPU is reserved for it cannot be told", short(t.AllocID), t.JobName, t.TaskName, t.Container, from, ns)})
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
		if limit, why := encoderLimit(e, p); limit > 0 && e.EncoderSessions >= limit {
			out = append(out, Finding{Level: WARN, Code: "encoder-saturated", Node: l.Node, GPU: g, Message: fmt.Sprintf("%d encoder sessions, at the limit of %d (%s), %.0f fps average", e.EncoderSessions, limit, why, e.EncoderFPS)})
		}
		if msg := hot(e, p); msg != "" {
			out = append(out, Finding{Level: WARN, Code: "hot", Node: l.Node, GPU: g, Message: msg})
		}
		if allReserved {
			out = append(out, Finding{Level: OK, Code: "held", Node: l.Node, GPU: g, Message: fmt.Sprintf("%d tenant(s), all reserved by Nomad, %d%% util", len(e.Tenants), e.UtilizationPct)})
		}
	}
	Sort(out)
	return out
}

// Sort orders findings worst first, keeping the order within a level.
func Sort(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool { return rank[fs[i].Level] > rank[fs[j].Level] })
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

// forHow is ", for 6h12m" when the history knows since when the GPU is in its state.
func forHow(l ledger.Ledger, e ledger.Entry) string {
	if e.StateSince == nil {
		return ""
	}
	return ", for " + Human(l.At.Sub(*e.StateSince))
}

// Human prints a duration the way an operator reads one: 45m, 6h12m, 2d1h.
func Human(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dd%dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
}

// encoderLimit is --encoder-max when given, else the card's published cap; 0 means no
// limit is known, and then no session count is a finding.
func encoderLimit(e ledger.Entry, p Policy) (int, string) {
	if p.EncoderMax < 0 {
		return 0, "" // --encoder-max -1: off
	}
	if p.EncoderMax > 0 {
		return p.EncoderMax, fmt.Sprintf("--encoder-max %d", p.EncoderMax)
	}
	if c, ok := cards.Lookup(e.Model); ok && c.SessionLimit > 0 {
		return c.SessionLimit, "the GeForce driver's cap, NVIDIA's support matrix"
	}
	return 0, ""
}

// hot trusts the driver first: an active thermal slowdown, or a margin to the card's
// own slowdown temperature at or below ThermalMarginMinC. Only when the driver reports
// neither does --temp-max decide.
func hot(e ledger.Entry, p Policy) string {
	if e.ThermalSlowdown != nil && *e.ThermalSlowdown {
		return fmt.Sprintf("thermal slowdown active at %d °C, %.0f W — the card is clocking itself down", e.TemperatureC, e.PowerW)
	}
	if e.ThermalMarginC != nil {
		if *e.ThermalMarginC <= ThermalMarginMinC {
			return fmt.Sprintf("%d °C, %d °C from the card's own slowdown temperature, %.0f W", e.TemperatureC, *e.ThermalMarginC, e.PowerW)
		}
		return ""
	}
	if e.ThermalSlowdown != nil {
		return ""
	}
	if p.TempMaxC > 0 && e.TemperatureC >= p.TempMaxC {
		return fmt.Sprintf("%d °C (--temp-max %d; the driver reports no thermal margin), %.0f W", e.TemperatureC, p.TempMaxC, e.PowerW)
	}
	return ""
}

// Code is one finding code and the level it is emitted at under a policy.
type Code struct {
	Code  string `json:"code"`
	Level Level  `json:"level"`
}

// Codes is every code Evaluate can emit, in the order the table in the README lists
// them, with its level under p — so the metrics can expose a zero for each.
func Codes(p Policy) []Code {
	unmanaged := BAD
	if p.AllowUnmanaged {
		unmanaged = WARN
	}
	return []Code{
		{"source-unavailable", ERROR}, {"unreserved-tenant", BAD}, {"unmanaged-tenant", unmanaged},
		{"contended", WARN}, {"reserved-idle", WARN}, {"encoder-saturated", WARN}, {"hot", WARN},
		{"idle", OK}, {"held", OK},
	}
}

// Rank is the level as a number, OK 0 to ERROR 3 — the exit code's scale.
func Rank(l Level) int { return rank[l] }
