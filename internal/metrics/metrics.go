// Package metrics renders the ledger in the Prometheus text exposition format, by hand:
// a handful of gauges does not earn a dependency.
package metrics

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/ledger"
)

func esc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func labels(kv ...string) string {
	parts := []string{}
	for i := 0; i+1 < len(kv); i += 2 {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, kv[i], esc(kv[i+1])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// family is one metric: its help text and its samples, written together because the
// exposition format wants a family's lines contiguous.
type family struct {
	name, help string
	samples    []string
}

func (f *family) add(labels string, value any) {
	f.samples = append(f.samples, fmt.Sprintf("%s%s %v", f.name, labels, value))
}

// Render writes every gauge; labels never carry a command, an env value or a path.
// fs are the ledger's findings and codes every code the policy can emit: each code
// gets a series, 0 when absent, so an alert on "> 0" has something to compare.
func Render(l ledger.Ledger, fs []findings.Finding, codes []findings.Code) string {
	fam := func(name, help string) *family { return &family{name: name, help: help} }
	up := fam("gpuledger_up", "1 when every source was read, 0 when one failed.")
	info := fam("gpuledger_gpu_info", "Static identity of the GPU.")
	util := fam("gpuledger_gpu_utilization_percent", "GPU utilisation as the driver reports it.")
	used := fam("gpuledger_gpu_memory_used_bytes", "Memory in use on the GPU.")
	total := fam("gpuledger_gpu_memory_total_bytes", "Memory on the GPU.")
	temp := fam("gpuledger_gpu_temperature_celsius", "GPU temperature.")
	power := fam("gpuledger_gpu_power_watts", "GPU power draw.")
	enc := fam("gpuledger_gpu_encoder_sessions", "Active NVENC sessions.")
	margin := fam("gpuledger_gpu_thermal_margin_celsius", "Degrees left before the card's own slowdown temperature, as the driver reports it.")
	slowdown := fam("gpuledger_gpu_thermal_slowdown", "1 while hardware or software thermal slowdown is active.")
	tenants := fam("gpuledger_gpu_tenants", "Distinct holders of the GPU (containers or host).")
	reservations := fam("gpuledger_gpu_reservations", "Nomad allocations the GPU is allocated to.")
	tmem := fam("gpuledger_tenant_memory_bytes", "Memory a tenant holds on a GPU.")
	fcount := fam("gpuledger_findings", "Findings of each code on the node at the last refresh.")
	worst := fam("gpuledger_worst_level", "The worst finding's level: 0 OK, 1 WARN, 2 BAD, 3 ERROR — check's exit-code scale.")
	tres := fam("gpuledger_tenant_reserved", "1 when Nomad allocated the GPU to the tenant's allocation.")
	state := fam("gpuledger_gpu_state", "1 for the state the GPU is in: free, reserved-idle, held or unaccounted.")
	since := fam("gpuledger_gpu_state_since_timestamp_seconds", "When the GPU entered its state, when serve keeps a --history.")

	upv := 1
	if len(l.Errors) > 0 {
		upv = 0
	}
	up.add(labels("node", l.Node), upv)
	if codes != nil {
		n := map[string]int{}
		for _, f := range fs {
			n[f.Code]++
		}
		for _, c := range codes {
			fcount.add(labels("node", l.Node, "code", c.Code, "level", string(c.Level)), n[c.Code])
		}
		worst.add(labels("node", l.Node), findings.Rank(findings.Worst(fs)))
	}
	for _, e := range l.Entries {
		g := labels("node", l.Node, "gpu", fmt.Sprint(e.Index), "uuid", e.UUID)
		info.add(labels("node", l.Node, "gpu", fmt.Sprint(e.Index), "uuid", e.UUID, "model", e.Model, "bus", e.BusID), 1)
		util.add(g, e.UtilizationPct)
		used.add(g, int64(e.MemoryUsedMiB)<<20)
		total.add(g, int64(e.MemoryTotalMiB)<<20)
		temp.add(g, e.TemperatureC)
		power.add(g, fmt.Sprintf("%g", e.PowerW))
		enc.add(g, e.EncoderSessions)
		if e.ThermalMarginC != nil {
			margin.add(g, *e.ThermalMarginC)
		}
		if e.ThermalSlowdown != nil {
			v := 0
			if *e.ThermalSlowdown {
				v = 1
			}
			slowdown.add(g, v)
		}
		tenants.add(g, len(e.Tenants))
		reservations.add(g, len(e.Reservations))
		st := e.State
		if st == "" {
			st = ledger.Classify(e)
		}
		sl := labels("node", l.Node, "gpu", fmt.Sprint(e.Index), "uuid", e.UUID, "state", string(st))
		state.add(sl, 1)
		if e.StateSince != nil {
			since.add(sl, e.StateSince.Unix())
		}
		ts := append([]ledger.Tenant(nil), e.Tenants...)
		sort.SliceStable(ts, func(i, j int) bool {
			if ts[i].Container != ts[j].Container {
				return ts[i].Container < ts[j].Container
			}
			return ts[i].ContainerID < ts[j].ContainerID
		})
		for _, t := range ts {
			tl := labels("node", l.Node, "gpu", fmt.Sprint(e.Index), "uuid", e.UUID, "kind", string(t.Kind), "container", t.Container, "container_id", t.ContainerID, "nomad_job", t.JobName, "task", t.TaskName, "alloc", t.AllocID, "namespace", t.Namespace)
			tmem.add(tl, int64(t.UsedMemoryMiB)<<20)
			r := 0
			if t.Reserved {
				r = 1
			}
			tres.add(tl, r)
		}
	}
	var b strings.Builder
	for _, f := range []*family{up, fcount, worst, info, util, used, total, temp, power, enc, margin, slowdown, tenants, reservations, state, since, tmem, tres} {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", f.name, f.help, f.name)
		for _, s := range f.samples {
			b.WriteString(s + "\n")
		}
	}
	return b.String()
}
