package main

import (
	"flag"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from the current output")

// timestamps are the only values that change from run to run.
var timestamp = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T[0-9:.]+(Z|[+-]\d{2}:\d{2})"`)

// The JSON is a contract (README: The JSON contract). These files are its reference: a
// change here is a change consumers see. Adding a field is fine, with -update; removing,
// renaming or changing a field's type or meaning also bumps ledger.Schema.
func TestJSONMatchesTheGoldenFiles(t *testing.T) {
	bin := build(t)
	for name, args := range map[string][]string{
		"ls":    {"ls", "--json"},
		"check": {"check", "--json"},
	} {
		out, _, _ := run(t, bin, args...)
		golden(t, name, out)
	}
	docker, nomadURL := fakes(t)
	root, _ := filepath.Abs("../../testdata")
	addr := freeAddr(t)
	srv := exec.Command(bin, "serve", "--listen", addr, "--nvidia-smi", filepath.Join(root, "fake-nvidia-smi.sh"), "--proc", filepath.Join(root, "proc"), "--docker", docker, "--nomad-addr", nomadURL, "--node", "gpud")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Process.Kill()
	waitFor(t, "http://"+addr+"/healthz")
	out, err := exec.Command(bin, "fleet", "ls", "--json", "--targets", addr).Output()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "fleet-ls", strings.ReplaceAll(string(out), addr, "ADDR"))
}

func golden(t *testing.T, name, out string) {
	t.Helper()
	got := timestamp.ReplaceAllString(out, `"TIME"`)
	path := filepath.Join("..", "..", "testdata", "golden", name+".json")
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run go test ./cmd/gpuledger -run Golden -update", err)
	}
	if got != string(want) {
		t.Errorf("%s JSON changed — if on purpose, go test ./cmd/gpuledger -run Golden -update, and bump ledger.Schema unless the change only adds fields\n got: %s\nwant: %s", name, got, want)
	}
	if !strings.Contains(got, `"schema":1`) {
		t.Errorf("%s JSON carries no schema: %s", name, got)
	}
}

func waitFor(t *testing.T, url string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if res, err := httpGet(url); err == nil {
			res.Body.Close()
			return
		}
		sleep100()
	}
	t.Fatalf("%s never answered", url)
}

var httpGet = http.Get

func sleep100() { time.Sleep(100 * time.Millisecond) }
