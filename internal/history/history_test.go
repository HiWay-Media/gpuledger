package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiway-media/gpuledger/internal/ledger"
	"github.com/hiway-media/gpuledger/internal/nvidia"
)

var t0 = time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)

func snap(at time.Time, states ...ledger.State) ledger.Ledger {
	l := ledger.Ledger{Node: "gpud", At: at, NomadRead: true}
	for i, s := range states {
		l.Entries = append(l.Entries, ledger.Entry{GPU: nvidia.GPU{Index: i, UUID: []string{"GPU-a", "GPU-b"}[i]}, State: s})
	}
	return l
}

func TestAStateKeepsItsSinceUntilItChanges(t *testing.T) {
	s := New(time.Minute)
	for i := 0; i < 10; i++ {
		s.Observe(snap(t0.Add(time.Duration(i)*15*time.Second), ledger.StateReservedIdle, ledger.StateHeld))
	}
	l := snap(t0.Add(150*time.Second), ledger.StateReservedIdle, ledger.StateFree)
	s.Observe(l)
	s.Annotate(&l)
	if l.Entries[0].StateSince == nil || !l.Entries[0].StateSince.Equal(t0) {
		t.Fatalf("unchanged state keeps the first observation: %v", l.Entries[0].StateSince)
	}
	if l.Entries[1].StateSince == nil || !l.Entries[1].StateSince.Equal(t0.Add(150*time.Second)) {
		t.Fatalf("a change starts a new since: %v", l.Entries[1].StateSince)
	}
}

// A gap longer than MaxGap — serve down, a node rebooted — means nothing is known about
// the time in between: the state starts again rather than claim a continuity.
func TestAGapResetsTheSince(t *testing.T) {
	s := New(time.Minute)
	s.Observe(snap(t0, ledger.StateReservedIdle))
	later := snap(t0.Add(10*time.Minute), ledger.StateReservedIdle)
	s.Observe(later)
	s.Annotate(&later)
	if !later.Entries[0].StateSince.Equal(t0.Add(10 * time.Minute)) {
		t.Fatalf("since after a gap: %v", later.Entries[0].StateSince)
	}
	// A ledger annotated long after the last observation knows no since at all.
	stale := snap(t0.Add(time.Hour), ledger.StateReservedIdle)
	s.Annotate(&stale)
	if stale.Entries[0].StateSince != nil {
		t.Fatalf("stale history must not annotate: %v", stale.Entries[0].StateSince)
	}
}

// A partial ledger (a source failed) says nothing reliable about states: it is not an
// observation, and the time it covers counts as a gap.
func TestPartialLedgersAreNotObservations(t *testing.T) {
	s := New(time.Minute)
	s.Observe(snap(t0, ledger.StateHeld))
	bad := snap(t0.Add(15*time.Second), ledger.StateFree)
	bad.Errors = []string{"nomad: down"}
	s.Observe(bad)
	ok := snap(t0.Add(30*time.Second), ledger.StateHeld)
	s.Annotate(&ok)
	if !ok.Entries[0].StateSince.Equal(t0) {
		t.Fatalf("a partial read in between must not reset a held state: %v", ok.Entries[0].StateSince)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "history.json")
	s := New(time.Minute)
	s.Observe(snap(t0, ledger.StateReservedIdle, ledger.StateFree))
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"GPU-a"`) || strings.Contains(string(b), "pid") {
		t.Fatalf("%s", b)
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp")); len(m) != 0 {
		t.Fatalf("the temporary file is renamed away: %v", m)
	}
	r, err := Load(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	l := snap(t0.Add(30*time.Second), ledger.StateReservedIdle, ledger.StateFree)
	r.Annotate(&l)
	if !l.Entries[0].StateSince.Equal(t0) || !l.Entries[1].StateSince.Equal(t0) {
		t.Fatalf("%v %v", l.Entries[0].StateSince, l.Entries[1].StateSince)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "none.json"), time.Minute); err != nil {
		t.Fatal("a missing file is an empty history:", err)
	}
	os.WriteFile(path, []byte("{not json"), 0o644)
	if _, err := Load(path, time.Minute); err == nil {
		t.Fatal("a corrupt file is an error")
	}
}

// A GPU gone from the node (a card pulled, a node rebuilt) is forgotten after Retain.
func TestGoneGPUsAreForgotten(t *testing.T) {
	s := New(time.Minute)
	s.Observe(snap(t0, ledger.StateFree, ledger.StateFree))
	s.Observe(snap(t0.Add(Retain+time.Hour), ledger.StateFree))
	if len(s.GPUs) != 1 {
		t.Fatalf("%v", s.GPUs)
	}
}
