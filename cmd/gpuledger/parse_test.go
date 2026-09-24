package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hiway-media/gpuledger/internal/findings"
	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

func TestParseRejectsWhatWouldBeSilentlyWrong(t *testing.T) {
	for _, args := range [][]string{
		{"check", "--exit-on", "Bad"},   // a typo would mean "always exit 0" in a gate
		{"check", "--exit-on", "fatal"}, // not a level
		{"serve", "--interval", "0"},    // the ticker panics on 0
		{"serve", "--interval", "-5s"},
		{"--json", "check"}, // flags first: "check" would be dropped and ls would run
		{"check", "extra"},
	} {
		if _, _, err := parse(args); err == nil {
			t.Errorf("parse(%q) must fail", args)
		}
	}
}

func TestParseDefaults(t *testing.T) {
	t.Setenv("NOMAD_ADDR", "")
	t.Setenv("DOCKER_HOST", "")
	cmd, o, err := parse(nil)
	if err != nil || cmd != "ls" || o.nomadAddr != "http://127.0.0.1:4646" || o.docker != "unix:///var/run/docker.sock" || o.tokenEnv != "NOMAD_TOKEN" || o.node == "" || o.exitOn != "" {
		t.Fatalf("%v %q %+v", err, cmd, o)
	}
	t.Setenv("NOMAD_ADDR", "http://nomad:4646")
	for _, lvl := range []string{"warn", "bad", "error"} {
		cmd, o, err = parse([]string{"check", "--exit-on", lvl})
		if err != nil || cmd != "check" || o.exitOn != lvl || o.nomadAddr != "http://nomad:4646" {
			t.Fatalf("%s: %v %+v", lvl, err, o)
		}
	}
}

func TestHandlers(t *testing.T) {
	l := ledger.Ledger{Node: "gpud", Entries: []ledger.Entry{{GPU: nvidia.GPU{Index: 0, UUID: "GPU-a", Model: "L4"}}}}
	get := func(path string) (int, string, string) {
		rec := httptest.NewRecorder()
		newMux(func() ledger.Ledger { return l }, findings.Default).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		b, _ := io.ReadAll(rec.Body)
		return rec.Code, rec.Header().Get("Content-Type"), string(b)
	}
	if code, ct, body := get("/metrics"); code != 200 || !strings.HasPrefix(ct, "text/plain; version=0.0.4") || !strings.Contains(body, `gpuledger_up{node="gpud"} 1`) {
		t.Fatalf("/metrics %d %s %s", code, ct, body)
	}
	if code, ct, body := get("/findings"); code != 200 || ct != "application/json" || !strings.Contains(body, `"code":"idle"`) || !strings.Contains(body, `"worst":"OK"`) {
		t.Fatalf("/findings %d %s %s", code, ct, body)
	}
	if code, _, body := get("/ledger"); code != 200 || !strings.Contains(body, `"uuid":"GPU-a"`) {
		t.Fatalf("/ledger %d %s", code, body)
	}
	if code, _, body := get("/healthz"); code != 200 || body != "ok\n" {
		t.Fatalf("/healthz %d %q", code, body)
	}
	l.Errors = []string{"nomad: HTTP 403"}
	if code, _, body := get("/healthz"); code != 503 || !strings.Contains(body, "nomad: HTTP 403") {
		t.Fatalf("/healthz with a source down must be 503 and say why: %d %q", code, body)
	}
	if code, _, _ := get("/nope"); code != 404 {
		t.Fatalf("unknown path: %d", code)
	}
}
