// Package render prints the ledger and the findings for a terminal.
package render

import (
	"fmt"
	"strings"

	"github.com/hiway-media/gpuledger/internal/findings"
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
		rows = append(rows, []string{fmt.Sprint(e.Index), e.Model, fmt.Sprintf("%d%%", e.UtilizationPct), fmt.Sprintf("%d/%d MiB", e.MemoryUsedMiB, e.MemoryTotalMiB), fmt.Sprintf("%d °C", e.TemperatureC), fmt.Sprint(e.EncoderSessions), strings.Join(res, ", "), strings.Join(tenants, "; ")})
	}
	head := fmt.Sprintf("gpuledger · %s · %d GPU(s) · %s", l.Node, len(l.Entries), l.At.UTC().Format("2006-01-02 15:04:05Z"))
	if len(l.Errors) > 0 {
		head += fmt.Sprintf(" · %d source error(s)", len(l.Errors))
	}
	return head + "\n" + table([]string{"gpu", "model", "util", "memory", "temp", "enc", "reserved by (nomad)", "tenants"}, rows)
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
