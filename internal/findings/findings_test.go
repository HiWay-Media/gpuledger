package findings

import (
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nomad"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

func codes(fs []Finding) string {
	s := []string{}
	for _, f := range fs {
		s = append(s, string(f.Level)+":"+f.Code)
	}
	return strings.Join(s, " ")
}

func TestEvaluateCoversEveryCode(t *testing.T) {
	l := ledger.Ledger{Node: "gpud", NomadRead: true, Errors: []string{"nomad: HTTP 500"}, Entries: []ledger.Entry{
		{GPU: nvidia.GPU{Index: 0, UUID: "GPU-aaaa", Model: "NVIDIA GeForce RTX 4090", EncoderSessions: 12, TemperatureC: 90}, Reservations: []nomad.Reservation{{AllocID: "r1", JobID: "j", Task: "t", DeviceIDs: []string{"GPU-aaaa"}}}, Tenants: []ledger.Tenant{
			{Kind: ledger.KindNomad, JobName: "j", TaskName: "t", AllocID: "r1", Reserved: true, Container: "c1"},
			{Kind: ledger.KindDocker, Container: "rogue", Image: "img", PIDs: []int{1}},
		}},
		{GPU: nvidia.GPU{Index: 1, UUID: "GPU-bbbb"}, Reservations: []nomad.Reservation{{AllocID: "r2", JobID: "j2", Task: "t2"}}},
		{GPU: nvidia.GPU{Index: 2, UUID: "GPU-cccc", MemoryTotalMiB: 8192}},
		{GPU: nvidia.GPU{Index: 3, UUID: "GPU-dddd"}, Tenants: []ledger.Tenant{{Kind: ledger.KindNomad, JobName: "j3", TaskName: "t3", AllocID: "r3", Reserved: false, AllocVisible: true, Container: "c3"}}},
		{GPU: nvidia.GPU{Index: 4, UUID: "GPU-eeee", UtilizationPct: 50}, Tenants: []ledger.Tenant{{Kind: ledger.KindNomad, JobName: "j4", TaskName: "t4", AllocID: "r4", Reserved: true, Container: "c4"}}},
	}}
	fs := Evaluate(l, Default)
	got := codes(fs)
	for _, want := range []string{"ERROR:source-unavailable", "BAD:unmanaged-tenant", "BAD:unreserved-tenant", "WARN:contended", "WARN:encoder-saturated", "WARN:hot", "WARN:reserved-idle", "OK:idle", "OK:held"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if !strings.HasPrefix(got, "ERROR:") || !strings.HasSuffix(got, "OK:held") {
		t.Fatalf("worst first, OK last: %s", got)
	}
	if Worst(fs) != ERROR {
		t.Fatal("worst must be ERROR")
	}
	if ExitCode(fs, "") != 0 || ExitCode(fs, "warn") != 3 || ExitCode(fs, "bad") != 3 || ExitCode(fs, "error") != 3 {
		t.Fatal("exit codes")
	}
	relaxed := Evaluate(ledger.Ledger{Entries: l.Entries[:1]}, Policy{EncoderMax: -1, TempMaxC: 0, AllowUnmanaged: true})
	if strings.Contains(codes(relaxed), "BAD") || strings.Contains(codes(relaxed), "hot") || strings.Contains(codes(relaxed), "encoder") {
		t.Fatalf("relaxed policy: %s", codes(relaxed))
	}
	only := Evaluate(ledger.Ledger{Entries: l.Entries[2:3]}, Policy{IdleIsFinding: false})
	if len(only) != 0 {
		t.Fatalf("idle suppressed: %s", codes(only))
	}
	if ExitCode(Evaluate(ledger.Ledger{Entries: l.Entries[4:5]}, Default), "warn") != 0 {
		t.Fatal("all OK exits 0 under any policy")
	}
}

func TestExitCodeUnderEveryPolicy(t *testing.T) {
	at := func(l Level) []Finding { return []Finding{{Level: OK}, {Level: l}} }
	cases := []struct {
		policy string
		worst  Level
		want   int
	}{
		{"", ERROR, 0}, {"", BAD, 0},
		{"warn", OK, 0}, {"warn", WARN, 1}, {"warn", BAD, 2}, {"warn", ERROR, 3},
		{"bad", OK, 0}, {"bad", WARN, 0}, {"bad", BAD, 2}, {"bad", ERROR, 3},
		{"error", OK, 0}, {"error", WARN, 0}, {"error", BAD, 0}, {"error", ERROR, 3},
	}
	for _, c := range cases {
		if got := ExitCode(at(c.worst), c.policy); got != c.want {
			t.Errorf("--exit-on %q at %s: got %d, want %d", c.policy, c.worst, got, c.want)
		}
	}
	if ExitCode(nil, "warn") != 0 || Worst(nil) != OK {
		t.Error("no findings is OK and exit 0")
	}
}

// A Nomad tenant is judged unreserved only when Nomad was read and returned its
// allocation; otherwise the ledger says it cannot tell, and why.
func TestUnreservedNeedsTheAllocationToBeVisible(t *testing.T) {
	tenant := ledger.Tenant{Kind: ledger.KindNomad, JobName: "enc", TaskName: "enc", AllocID: "abcdef12-0000", Namespace: "video", Container: "enc-abcdef12"}
	entry := func(tn ledger.Tenant) []ledger.Entry {
		return []ledger.Entry{{GPU: nvidia.GPU{Index: 0, UUID: "GPU-aaaa"}, Tenants: []ledger.Tenant{tn}}}
	}
	got := codes(Evaluate(ledger.Ledger{Node: "n", NomadRead: true, Entries: entry(tenant)}, Default))
	if got != "ERROR:source-unavailable" {
		t.Fatalf("alloc not returned: %s", got)
	}
	fs := Evaluate(ledger.Ledger{Node: "n", NomadRead: true, Entries: entry(tenant)}, Default)
	if !strings.Contains(fs[0].Message, "read-job") || !strings.Contains(fs[0].Message, `"video"`) || !strings.Contains(fs[0].Message, "abcdef12") {
		t.Fatalf("the message must name the namespace and the capability: %s", fs[0].Message)
	}
	if got := codes(Evaluate(ledger.Ledger{Node: "n", Entries: entry(tenant)}, Default)); got != "" {
		t.Fatalf("Nomad not read: no verdict on reservation, got %s", got)
	}
	tenant.AllocVisible = true
	if got := codes(Evaluate(ledger.Ledger{Node: "n", NomadRead: true, Entries: entry(tenant)}, Default)); got != "BAD:unreserved-tenant" {
		t.Fatalf("alloc returned without this GPU: %s", got)
	}
}

func TestDurationsFromHistoryInTheMessages(t *testing.T) {
	at := time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { t := at.Add(-d); return &t }
	l := ledger.Ledger{Node: "gpud", At: at, NomadRead: true, Entries: []ledger.Entry{
		{GPU: nvidia.GPU{Index: 0, UUID: "GPU-a"}, Reservations: []nomad.Reservation{{AllocID: "r1", JobID: "worker", Task: "w"}}, State: ledger.StateReservedIdle, StateSince: ago(6*time.Hour + 12*time.Minute)},
		{GPU: nvidia.GPU{Index: 1, UUID: "GPU-b"}, State: ledger.StateFree, StateSince: ago(50 * time.Hour)},
		{GPU: nvidia.GPU{Index: 2, UUID: "GPU-c"}, State: ledger.StateFree},
	}}
	msgs := map[string]string{}
	for _, f := range Evaluate(l, Default) {
		msgs[f.GPU] = f.Message
	}
	if !strings.Contains(msgs["gpu0 GPU-a"], "for 6h12m") || !strings.Contains(msgs["gpu1 GPU-b"], "for 2d2h") || strings.Contains(msgs["gpu2 GPU-c"], " for ") {
		t.Fatalf("%v", msgs)
	}
	for d, want := range map[time.Duration]string{30 * time.Second: "<1m", 45 * time.Minute: "45m", 3 * time.Hour: "3h0m", 49 * time.Hour: "2d1h"} {
		if got := Human(d); got != want {
			t.Errorf("Human(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestEncoderLimitIsTheCards(t *testing.T) {
	g := func(model string, sessions int) ledger.Ledger {
		return ledger.Ledger{Node: "n", NomadRead: true, Entries: []ledger.Entry{{GPU: nvidia.GPU{Index: 0, UUID: "GPU-a", Model: model, EncoderSessions: sessions}, Tenants: []ledger.Tenant{{Kind: ledger.KindNomad, Reserved: true, AllocVisible: true}}}}}
	}
	has := func(l ledger.Ledger, p Policy) string {
		for _, f := range Evaluate(l, p) {
			if f.Code == "encoder-saturated" {
				return f.Message
			}
		}
		return ""
	}
	if m := has(g("Quadro RTX 4000", 40), Default); m != "" {
		t.Errorf("sessions are unrestricted on a Quadro RTX 4000: %s", m)
	}
	if m := has(g("NVIDIA GeForce RTX 4090", 12), Default); !strings.Contains(m, "12") || !strings.Contains(m, "GeForce") {
		t.Errorf("GeForce at its cap: %q", m)
	}
	if m := has(g("NVIDIA GeForce RTX 4090", 11), Default); m != "" {
		t.Errorf("under the cap: %s", m)
	}
	if m := has(g("Quadro RTX 4000", 8), Policy{EncoderMax: 8}); !strings.Contains(m, "--encoder-max 8") {
		t.Errorf("an explicit --encoder-max applies to every card: %q", m)
	}
	if m := has(g("Unknown Card", 50), Default); m != "" {
		t.Errorf("no limit known, no finding: %s", m)
	}
}

func TestHotTrustsTheDriverBeforeTheFlag(t *testing.T) {
	i := func(v int) *int { return &v }
	b := func(v bool) *bool { return &v }
	hot := func(g nvidia.GPU) string {
		g.UUID = "GPU-a"
		for _, f := range Evaluate(ledger.Ledger{Node: "n", Entries: []ledger.Entry{{GPU: g}}}, Default) {
			if f.Code == "hot" {
				return f.Message
			}
		}
		return ""
	}
	if m := hot(nvidia.GPU{TemperatureC: 70, ThermalSlowdown: b(true)}); !strings.Contains(m, "thermal slowdown") {
		t.Errorf("slowdown active: %q", m)
	}
	if m := hot(nvidia.GPU{TemperatureC: 80, ThermalMarginC: i(4)}); !strings.Contains(m, "4 °C") {
		t.Errorf("within the margin: %q", m)
	}
	if m := hot(nvidia.GPU{TemperatureC: 90, ThermalMarginC: i(20), ThermalSlowdown: b(false)}); m != "" {
		t.Errorf("the driver says 20 °C of margin and no slowdown; --temp-max does not override it: %q", m)
	}
	if m := hot(nvidia.GPU{TemperatureC: 90}); !strings.Contains(m, "--temp-max") {
		t.Errorf("no driver answer, the flag decides: %q", m)
	}
}
