package metrics

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nomad"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

// everything is a ledger that makes Render emit every family and every label.
func everything() string {
	m, on, since := 3, true, time.Unix(1790000000, 0)
	l := ledger.Ledger{Node: "gpud", NomadRead: true, Entries: []ledger.Entry{{
		GPU:          nvidia.GPU{Index: 0, UUID: "GPU-a", Model: "L4", BusID: "0", ThermalMarginC: &m, ThermalSlowdown: &on},
		Reservations: []nomad.Reservation{{AllocID: "a"}},
		Tenants:      []ledger.Tenant{{Kind: ledger.KindNomad, Container: "c", ContainerID: "id", JobName: "j", TaskName: "t", AllocID: "a", Namespace: "default", Reserved: true}},
		State:        ledger.StateReservedIdle, StateSince: &since,
	}}}
	return Render(l, findings.Evaluate(l, findings.Default), findings.Codes(findings.Default))
}

var (
	selectorRe = regexp.MustCompile(`(gpuledger_[a-z_]+)(\{[^}]*\})?`)
	labelKeyRe = regexp.MustCompile(`([a-z_]+)\s*(=~|!~|!=|=)`)
)

// The alert rules and the dashboard are part of the metrics' contract: every metric
// they name must exist, and every label they select on must be one it carries — a
// rename in Render must fail here, not silence an alert.
func TestRulesAndDashboardNameRealMetrics(t *testing.T) {
	out := everything()
	labelsOf := map[string]map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line[:strings.IndexAny(line, "{ ")]
		if labelsOf[name] == nil {
			labelsOf[name] = map[string]bool{}
		}
		for _, kv := range labelKeyRe.FindAllStringSubmatch(line[len(name):], -1) {
			labelsOf[name][kv[1]] = true
		}
	}
	for _, path := range []string{"../../deploy/prometheus/gpuledger.rules.yml", "../../deploy/grafana/gpuledger.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(path, ".json") {
			var d map[string]any
			if err := json.Unmarshal(b, &d); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
		n := 0
		for _, m := range selectorRe.FindAllStringSubmatch(string(b), -1) {
			name := m[1]
			if strings.HasSuffix(name, "_") { // a prose mention like gpuledger_gpu_*
				continue
			}
			n++
			have, ok := labelsOf[name]
			if !ok {
				t.Errorf("%s names %s, which /metrics does not expose", path, name)
				continue
			}
			for _, kv := range labelKeyRe.FindAllStringSubmatch(m[2], -1) {
				if !have[kv[1]] {
					t.Errorf("%s selects %s on label %q, which it does not carry", path, name, kv[1])
				}
			}
		}
		if n == 0 {
			t.Errorf("%s names no metric", path)
		}
	}
}

// Prometheus attaches job and instance to every scraped series; a metric label of the
// same name is renamed exported_job on ingestion, and the value the dashboard and the
// rules select on is gone. Found with the dashboard against a real Prometheus.
func TestNoLabelCollidesWithPrometheusTargetLabels(t *testing.T) {
	for _, line := range strings.Split(everything(), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, kv := range labelKeyRe.FindAllStringSubmatch(line, -1) {
			if kv[1] == "job" || kv[1] == "instance" {
				t.Fatalf("label %q collides with Prometheus's target label: %s", kv[1], line)
			}
		}
	}
}
