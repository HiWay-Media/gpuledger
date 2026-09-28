package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/fleet"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nomad"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

var at = time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)

func sample() ledger.Ledger {
	since := at.Add(-90 * time.Minute)
	return ledger.Ledger{Schema: 1, Node: "gpud", At: at, NomadRead: true, Entries: []ledger.Entry{
		{GPU: nvidia.GPU{Index: 0, UUID: "GPU-aaaa", Model: "Quadro RTX 4000", MemoryUsedMiB: 2048, MemoryTotalMiB: 8192, UtilizationPct: 72, TemperatureC: 66, EncoderSessions: 3},
			Tenants: []ledger.Tenant{{Kind: ledger.KindDocker, Container: `<script>alert("x")</script>`, UsedMemoryMiB: 2048}}, State: ledger.StateUnaccounted},
		{GPU: nvidia.GPU{Index: 1, UUID: "GPU-bbbb", Model: "Quadro RTX 4000", MemoryTotalMiB: 8192},
			Reservations: []nomad.Reservation{{AllocID: "a1", JobID: "worker", Task: "w"}}, State: ledger.StateReservedIdle, StateSince: &since},
	}}
}

func TestNodePage(t *testing.T) {
	l := sample()
	b, err := NodePage(l, findings.Evaluate(l, findings.Default), 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{
		`<meta http-equiv="refresh" content="15">`,
		`<link rel="stylesheet" href="style.css">`,
		"gpud", "Quadro RTX 4000", "72%", "2048/8192 MiB", "66 °C",
		"reserved-idle", "1h30m", "worker/w",
		`class="level BAD"`, "unmanaged-tenant",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Whatever a container is named, it is text on the page.
	if strings.Contains(page, "<script") || !strings.Contains(page, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;") {
		t.Fatalf("a container name full of markup must render as text:\n%s", page)
	}
	if strings.Contains(page, "style=") || strings.Contains(page, "<style") || strings.Contains(page, "onclick") {
		t.Fatal("no inline style or handler: the CSP allows neither")
	}
}

func TestSourceErrorsAreOnThePage(t *testing.T) {
	l := sample()
	l.Errors = []string{"nomad: HTTP 403 — the token … needs node:read"}
	b, _ := NodePage(l, findings.Evaluate(l, findings.Default), time.Minute)
	if !strings.Contains(string(b), "nomad: HTTP 403") || !strings.Contains(string(b), `class="level ERROR"`) {
		t.Fatalf("%s", b)
	}
}

func TestSecure(t *testing.T) {
	h := Secure(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }))
	for _, m := range []string{"GET", "HEAD"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/", nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d", m, rec.Code)
		}
		hd := rec.Header()
		csp := hd.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "style-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe") || strings.Contains(csp, "script-src") {
			t.Fatalf("CSP: %q", csp)
		}
		if hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Referrer-Policy") != "no-referrer" || hd.Get("X-Frame-Options") != "DENY" {
			t.Fatalf("headers: %v", hd)
		}
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/", nil))
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" || rec.Body.String() == "ok" {
			t.Fatalf("%s must be refused: %d %v", m, rec.Code, rec.Header())
		}
	}
}

func TestStyleCarriesBothThemes(t *testing.T) {
	if !strings.Contains(Style, "prefers-color-scheme: dark") || !strings.Contains(Style, ".level.BAD") {
		t.Fatal(Style)
	}
}

func TestFleetPage(t *testing.T) {
	s := fleet.Summary{Schema: 1,
		Nodes: []fleet.NodeSummary{{Node: "gpud", Schema: 1, GPUs: 2, Held: 1, ReservedIdle: 1, MemoryUsedMiB: 1024, MemoryTotalMiB: 16384}, {Node: `gpuf"><b>x`, Err: "connection refused"}},
		Jobs:  []fleet.JobSummary{{Namespace: "video", Job: "worker", Reserved: 2, Held: 1}},
		Total: fleet.NodeSummary{Node: "fleet", Schema: 1, GPUs: 2, Held: 1, ReservedIdle: 1, Unreachable: 1},
	}
	urls := map[string]string{"gpud": "http://10.0.0.4:9877"}
	fs := []findings.Finding{{Level: findings.ERROR, Code: "source-unavailable", Node: "gpuf", Message: "down"}}
	b, err := FleetPage(s, fs, urls, at, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{`content="30"`, `<a href="http://10.0.0.4:9877/">gpud</a>`, "video/worker", "unreachable", "connection refused", `class="level ERROR"`, "1 unreachable"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(page, "<b>x") {
		t.Fatalf("a node name is text: %s", page)
	}
}
