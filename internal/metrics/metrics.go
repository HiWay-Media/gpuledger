// Package metrics renders the ledger in the Prometheus text exposition format, by hand:
// a handful of gauges does not earn a dependency.
package metrics

import (
	"fmt"
	"sort"
	"strings"

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

// Render writes every gauge; labels never carry a command, an env value or a path.
func Render(l ledger.Ledger) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("# HELP gpuledger_up 1 when every source was read, 0 when one failed.\n# TYPE gpuledger_up gauge\n")
	up := 1
	if len(l.Errors) > 0 {
		up = 0
	}
	w("gpuledger_up%s %d\n", labels("node", l.Node), up)
	w("# HELP gpuledger_gpu_info Static identity of the GPU.\n# TYPE gpuledger_gpu_info gauge\n")
	w("# HELP gpuledger_gpu_utilization_percent GPU utilisation as the driver reports it.\n# TYPE gpuledger_gpu_utilization_percent gauge\n")
	w("# HELP gpuledger_gpu_memory_used_bytes Memory in use on the GPU.\n# TYPE gpuledger_gpu_memory_used_bytes gauge\n")
	w("# HELP gpuledger_gpu_memory_total_bytes Memory on the GPU.\n# TYPE gpuledger_gpu_memory_total_bytes gauge\n")
	w("# HELP gpuledger_gpu_temperature_celsius GPU temperature.\n# TYPE gpuledger_gpu_temperature_celsius gauge\n")
	w("# HELP gpuledger_gpu_power_watts GPU power draw.\n# TYPE gpuledger_gpu_power_watts gauge\n")
	w("# HELP gpuledger_gpu_encoder_sessions Active NVENC sessions.\n# TYPE gpuledger_gpu_encoder_sessions gauge\n")
	w("# HELP gpuledger_gpu_tenants Distinct holders of the GPU (containers or host).\n# TYPE gpuledger_gpu_tenants gauge\n")
	w("# HELP gpuledger_gpu_reservations Nomad allocations the GPU is allocated to.\n# TYPE gpuledger_gpu_reservations gauge\n")
	w("# HELP gpuledger_tenant_memory_bytes Memory a tenant holds on a GPU.\n# TYPE gpuledger_tenant_memory_bytes gauge\n")
	w("# HELP gpuledger_tenant_reserved 1 when Nomad allocated the GPU to the tenant's allocation.\n# TYPE gpuledger_tenant_reserved gauge\n")
	for _, e := range l.Entries {
		g := labels("node", l.Node, "gpu", fmt.Sprint(e.Index), "uuid", e.UUID)
		w("gpuledger_gpu_info%s 1\n", labels("node", l.Node, "gpu", fmt.Sprint(e.Index), "uuid", e.UUID, "model", e.Model, "bus", e.BusID))
		w("gpuledger_gpu_utilization_percent%s %d\n", g, e.UtilizationPct)
		w("gpuledger_gpu_memory_used_bytes%s %d\n", g, int64(e.MemoryUsedMiB)<<20)
		w("gpuledger_gpu_memory_total_bytes%s %d\n", g, int64(e.MemoryTotalMiB)<<20)
		w("gpuledger_gpu_temperature_celsius%s %d\n", g, e.TemperatureC)
		w("gpuledger_gpu_power_watts%s %g\n", g, e.PowerW)
		w("gpuledger_gpu_encoder_sessions%s %d\n", g, e.EncoderSessions)
		w("gpuledger_gpu_tenants%s %d\n", g, len(e.Tenants))
		w("gpuledger_gpu_reservations%s %d\n", g, len(e.Reservations))
		ts := append([]ledger.Tenant(nil), e.Tenants...)
		sort.SliceStable(ts, func(i, j int) bool { return ts[i].Container < ts[j].Container })
		for _, t := range ts {
			tl := labels("node", l.Node, "gpu", fmt.Sprint(e.Index), "uuid", e.UUID, "kind", string(t.Kind), "container", t.Container, "job", t.JobName, "task", t.TaskName, "alloc", t.AllocID, "namespace", t.Namespace)
			w("gpuledger_tenant_memory_bytes%s %d\n", tl, int64(t.UsedMemoryMiB)<<20)
			r := 0
			if t.Reserved {
				r = 1
			}
			w("gpuledger_tenant_reserved%s %d\n", tl, r)
		}
	}
	return b.String()
}
