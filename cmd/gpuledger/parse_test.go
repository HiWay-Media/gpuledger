package main

import (
	"io"
	"net/http/httptest"
	"os"
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

func TestParseFleet(t *testing.T) {
	t.Setenv("CONSUL_HTTP_ADDR", "")
	cmd, o, err := parse([]string{"fleet", "--targets", "gpud:9877"})
	if err != nil || cmd != "fleet" || o.sub != "ls" || o.targets != "gpud:9877" || o.consulService != "gpuledger" || o.consulTokenEnv != "CONSUL_HTTP_TOKEN" || o.timeout <= 0 {
		t.Fatalf("%v %q %+v", err, cmd, o)
	}
	if _, o, err = parse([]string{"fleet", "check", "--consul", "127.0.0.1:8500", "--exit-on", "bad"}); err != nil || o.sub != "check" || o.consul != "127.0.0.1:8500" {
		t.Fatalf("%v %+v", err, o)
	}
	t.Setenv("CONSUL_HTTP_ADDR", "consul:8500")
	if _, o, err = parse([]string{"fleet"}); err != nil || o.consul != "consul:8500" {
		t.Fatalf("CONSUL_HTTP_ADDR is the default: %v %+v", err, o)
	}
	t.Setenv("CONSUL_HTTP_ADDR", "")
	if _, o, err = parse([]string{"fleet", "--nomad-service", "gpuledger"}); err != nil || o.nomadService != "gpuledger" || o.nomadNamespace != "default" {
		t.Fatalf("Nomad's service discovery is enough: %v %+v", err, o)
	}
	for _, args := range [][]string{
		{"fleet"},                                // nowhere to look
		{"fleet", "rm", "--targets", "a:1"},      // not a fleet command
		{"fleet", "ls", "x", "--targets", "a:1"}, // stray argument
		{"fleet", "--targets", "a:1", "--timeout", "0"},
	} {
		if _, _, err := parse(args); err == nil {
			t.Errorf("parse(%q) must fail", args)
		}
	}
}

func TestPodmanDefaultIsTheSocketWhenItExists(t *testing.T) {
	if got := podmanEndpoint("auto", "/definitely/not/here.sock"); got != "" {
		t.Fatalf("no socket, no podman: %q", got)
	}
	sock := t.TempDir() + "/podman.sock"
	os.WriteFile(sock, nil, 0o600)
	if got := podmanEndpoint("auto", sock); got != "unix://"+sock {
		t.Fatalf("%q", got)
	}
	if got := podmanEndpoint("off", sock); got != "" {
		t.Fatalf("off: %q", got)
	}
	if got := podmanEndpoint("http://h:1", sock); got != "http://h:1" {
		t.Fatalf("explicit: %q", got)
	}
}

func TestNomadTLSFlagsDefaultToTheCLIsVariables(t *testing.T) {
	t.Setenv("NOMAD_CACERT", "/etc/nomad.d/ca.pem")
	t.Setenv("NOMAD_CLIENT_CERT", "/etc/nomad.d/cli.pem")
	t.Setenv("NOMAD_CLIENT_KEY", "/etc/nomad.d/cli-key.pem")
	_, o, err := parse([]string{"check"})
	if err != nil || o.nomadTLS.CACert != "/etc/nomad.d/ca.pem" || o.nomadTLS.ClientCert != "/etc/nomad.d/cli.pem" || o.nomadTLS.ClientKey != "/etc/nomad.d/cli-key.pem" {
		t.Fatalf("%v %+v", err, o.nomadTLS)
	}
	_, o, _ = parse([]string{"check", "--nomad-ca-cert", "/x/ca.pem", "--nomad-tls-server-name", "server.global.nomad"})
	if o.nomadTLS.CACert != "/x/ca.pem" || o.nomadTLS.ServerName != "server.global.nomad" || o.nomadTLS.ClientCert != "/etc/nomad.d/cli.pem" {
		t.Fatalf("a flag wins over its variable: %+v", o.nomadTLS)
	}
}

func TestConsulTLSFlagsDefaultToTheCLIsVariables(t *testing.T) {
	t.Setenv("CONSUL_CACERT", "/etc/consul.d/ca.pem")
	t.Setenv("CONSUL_CLIENT_CERT", "/etc/consul.d/cli.pem")
	t.Setenv("CONSUL_CLIENT_KEY", "/etc/consul.d/cli-key.pem")
	_, o, err := parse([]string{"fleet", "--consul", "consul:8501", "--consul-tls-server-name", "server.dc1.consul"})
	if err != nil || o.consulTLS.CACert != "/etc/consul.d/ca.pem" || o.consulTLS.ClientKey != "/etc/consul.d/cli-key.pem" || o.consulTLS.ServerName != "server.dc1.consul" {
		t.Fatalf("%v %+v", err, o.consulTLS)
	}
}
