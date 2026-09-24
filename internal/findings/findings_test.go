package findings

import (
	"strings"
	"testing"

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
	l := ledger.Ledger{Node: "gpud", Errors: []string{"nomad: HTTP 500"}, Entries: []ledger.Entry{
		{GPU: nvidia.GPU{Index: 0, UUID: "GPU-aaaa", EncoderSessions: 9, TemperatureC: 90}, Reservations: []nomad.Reservation{{AllocID: "r1", JobID: "j", Task: "t", DeviceIDs: []string{"GPU-aaaa"}}}, Tenants: []ledger.Tenant{
			{Kind: ledger.KindNomad, JobName: "j", TaskName: "t", AllocID: "r1", Reserved: true, Container: "c1"},
			{Kind: ledger.KindDocker, Container: "rogue", Image: "img", PIDs: []int{1}},
		}},
		{GPU: nvidia.GPU{Index: 1, UUID: "GPU-bbbb"}, Reservations: []nomad.Reservation{{AllocID: "r2", JobID: "j2", Task: "t2"}}},
		{GPU: nvidia.GPU{Index: 2, UUID: "GPU-cccc", MemoryTotalMiB: 8192}},
		{GPU: nvidia.GPU{Index: 3, UUID: "GPU-dddd"}, Tenants: []ledger.Tenant{{Kind: ledger.KindNomad, JobName: "j3", TaskName: "t3", AllocID: "r3", Reserved: false, Container: "c3"}}},
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
	relaxed := Evaluate(ledger.Ledger{Entries: l.Entries[:1]}, Policy{EncoderMax: 0, TempMaxC: 0, AllowUnmanaged: true})
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
