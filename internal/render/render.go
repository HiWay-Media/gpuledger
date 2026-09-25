// Package render prints the ledger and the findings for a terminal.
package render

import (
	"fmt"
	"strings"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/fleet"
	"github.com/hiway-media/gpuledger/internal/ledger"
)

func table(headers []string, rows [][]string) string {
	w := make([]int, len(headers))
	for i, h := range headers {
		w[i] = len([]rune(h))
	}
	for _, r := range rows {
		for i, c := range r {
			if n := len([]rune(c)); n > w[i] {
				w[i] = n
			}
		}
	}
	line := func(cells []string) string {
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = c + strings.Repeat(" ", w[i]-len([]rune(c)))
		}
		return "│ " + strings.Join(parts, " │ ") + " │"
	}
	sep := make([]string, len(w))
	for i, n := range w {
		sep[i] = strings.Repeat("─", n+2)
	}
	out := []string{line(headers), "├" + strings.Join(sep, "┼") + "┤"}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

// Ledger prints one row per GPU with its tenants compressed to "kind:name (mem)".
func Ledger(l ledger.Ledger) string {
	rows := [][]string{}
	for _, e := range l.Entries {
		tenants := []string{}
		for _, t := range e.Tenants {
			name := t.Container
			if t.Kind == ledger.KindNomad && t.JobName != "" {
				name = t.JobName + "/" + t.TaskName
			}
			if name == "" {
				name = strings.Join(t.Processes, ",")
			}
			mark := ""
			if t.Kind == ledger.KindNomad && !t.Reserved {
				mark = " !unreserved"
			}
			if t.Kind != ledger.KindNomad {
				mark = " !unmanaged"
			}
			tenants = append(tenants, fmt.Sprintf("%s:%s (%d MiB)%s", t.Kind, name, t.UsedMemoryMiB, mark))
		}
		res := []string{}
		for _, r := range e.Reservations {
			res = append(res, r.JobID+"/"+r.Task)
		}
		state := string(e.State)
		if state == "" {
			state = string(ledger.Classify(e))
		}
		if e.StateSince != nil {
			state += " " + findings.Human(l.At.Sub(*e.StateSince))
		}
		rows = append(rows, []string{fmt.Sprint(e.Index), e.Model, fmt.Sprintf("%d%%", e.UtilizationPct), fmt.Sprintf("%d/%d MiB", e.MemoryUsedMiB, e.MemoryTotalMiB), fmt.Sprintf("%d °C", e.TemperatureC), fmt.Sprint(e.EncoderSessions), state, strings.Join(res, ", "), strings.Join(tenants, "; ")})
	}
	head := fmt.Sprintf("gpuledger · %s · %d GPU(s) · %s", l.Node, len(l.Entries), l.At.UTC().Format("2006-01-02 15:04:05Z"))
	if len(l.Errors) > 0 {
		head += fmt.Sprintf(" · %d source error(s)", len(l.Errors))
	}
	return head + "\n" + table([]string{"gpu", "model", "util", "memory", "temp", "enc", "state", "reserved by (nomad)", "tenants"}, rows)
}

var glyph = map[findings.Level]string{findings.OK: "🟢 OK   ", findings.WARN: "🟡 WARN ", findings.BAD: "🔴 BAD  ", findings.ERROR: "⚫ ERROR"}

// Findings prints the verdicts, worst first, and a one-line tally.
func Findings(fs []findings.Finding) string {
	var b strings.Builder
	tally := map[findings.Level]int{}
	for _, f := range fs {
		tally[f.Level]++
		g := f.GPU
		if g == "" {
			g = f.Node
		}
		fmt.Fprintf(&b, "%s %-18s %-22s %s\n", glyph[f.Level], f.Code, g, f.Message)
	}
	fmt.Fprintf(&b, "\n%d findings: %d OK, %d WARN, %d BAD, %d ERROR\n", len(fs), tally[findings.OK], tally[findings.WARN], tally[findings.BAD], tally[findings.ERROR])
	return b.String()
}

// Fleet prints the per-node table with the fleet's total as its last row, then the
// per-job table: GPUs each job was reserved and how many of those it holds.
func Fleet(s fleet.Summary) string {
	nodes := len(s.Nodes)
	head := fmt.Sprintf("gpuledger fleet · %d node(s)", nodes)
	if s.Total.Unreachable > 0 {
		head += fmt.Sprintf(", %d unreachable", s.Total.Unreachable)
	}
	head += fmt.Sprintf(" · %d GPU(s)", s.Total.GPUs)
	row := func(n fleet.NodeSummary) []string {
		if n.Err != "" {
			return []string{n.Node, "—", "—", "—", "—", "—", "unreachable: " + n.Err}
		}
		name := n.Node
		if n.Node != "fleet" && n.Schema != ledger.Schema {
			name += fmt.Sprintf(" (schema %d)", n.Schema) // a node on another gpuledger version
		}
		return []string{name, fmt.Sprint(n.GPUs), fmt.Sprint(n.Held), fmt.Sprint(n.ReservedIdle), fmt.Sprint(n.Unaccounted), fmt.Sprint(n.Free), fmt.Sprintf("%d/%d MiB", n.MemoryUsedMiB, n.MemoryTotalMiB)}
	}
	rows := [][]string{}
	for _, n := range s.Nodes {
		rows = append(rows, row(n))
	}
	rows = append(rows, row(s.Total))
	out := head + "\n" + table([]string{"node", "gpus", "held", "reserved-idle", "unaccounted", "free", "memory"}, rows)
	if len(s.Jobs) > 0 {
		jobs := [][]string{}
		for _, j := range s.Jobs {
			jobs = append(jobs, []string{j.Namespace + "/" + j.Job, fmt.Sprint(j.Reserved), fmt.Sprint(j.Held)})
		}
		out += "\n\n" + table([]string{"job", "reserved", "held"}, jobs)
	}
	return out
}

// FleetFindings is Findings with the node in its own column.
func FleetFindings(fs []findings.Finding) string {
	w := 6
	for _, f := range fs {
		if n := len([]rune(f.Node)); n > w {
			w = n
		}
	}
	var b strings.Builder
	tally := map[findings.Level]int{}
	for _, f := range fs {
		tally[f.Level]++
		fmt.Fprintf(&b, "%s %-18s %-*s %-22s %s\n", glyph[f.Level], f.Code, w, f.Node, f.GPU, f.Message)
	}
	fmt.Fprintf(&b, "\n%d findings: %d OK, %d WARN, %d BAD, %d ERROR\n", len(fs), tally[findings.OK], tally[findings.WARN], tally[findings.BAD], tally[findings.ERROR])
	return b.String()
}
