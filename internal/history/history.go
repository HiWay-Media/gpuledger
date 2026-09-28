// Package history remembers, per GPU, which state it is in and since when, so a
// finding can say "reserved-idle for 6h" — the number a scheduling decision needs. It
// keeps one record per GPU, not a stream of snapshots: the state, when it was entered,
// when it was last seen. Only serve writes it; ls and check read it.
package history

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/hiway-media/gpuledger/internal/ledger"
)

// Retain is how long a GPU no longer on the node is remembered.
const Retain = 7 * 24 * time.Hour

// Record is one GPU's state and its times. Nothing about tenants is kept: Uses names
// the jobs that reserved it at the last observation, and whether each held it.
type Record struct {
	Node    string                   `json:"node"`
	Index   int                      `json:"index"`
	State   ledger.State             `json:"state"`
	Since   time.Time                `json:"since"`
	Seen    time.Time                `json:"seen"`
	Seconds map[ledger.State]float64 `json:"seconds"`
	Uses    []Use                    `json:"uses,omitempty"`
}

// Use is one job's claim on a GPU at an observation.
type Use struct {
	Namespace string `json:"namespace"`
	Job       string `json:"job"`
	Held      bool   `json:"held"`
}

// Job is one job's GPU-seconds, reserved and in use (Held) or reserved and not (Idle).
type Job struct {
	Namespace string    `json:"namespace"`
	Job       string    `json:"job"`
	Held      float64   `json:"held"`
	Idle      float64   `json:"idle"`
	Seen      time.Time `json:"seen"` // last observed reserving a GPU
}

// Store is the history, by GPU UUID. MaxGap is the longest silence between two
// observations still read as continuous — three refresh intervals for serve.
type Store struct {
	mu     sync.Mutex
	Schema int                `json:"schema"`
	GPUs   map[string]*Record `json:"gpus"`
	Jobs   map[string]*Job    `json:"jobs"` // by namespace/job
	MaxGap time.Duration      `json:"-"`
}

// New is an empty history.
func New(maxGap time.Duration) *Store {
	return &Store{GPUs: map[string]*Record{}, Jobs: map[string]*Job{}, MaxGap: maxGap}
}

// Observe records a ledger. A partial ledger — a source failed — is not an
// observation: its states may be wrong, and the time it covers becomes a gap.
//
// The time since the previous observation, when it is no gap, is added to the state
// seen then and to the jobs that reserved the GPU then — the interval belongs to what
// was known at its start. A gap is added to nothing.
func (s *Store) Observe(l ledger.Ledger) {
	if len(l.Errors) > 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range l.Entries {
		st := e.State
		if st == "" {
			st = ledger.Classify(e)
		}
		r := s.GPUs[e.UUID]
		if r == nil {
			r = &Record{Node: l.Node, State: st, Since: l.At, Seen: l.At, Seconds: map[ledger.State]float64{}}
			s.GPUs[e.UUID] = r
		}
		if r.Seconds == nil {
			r.Seconds = map[ledger.State]float64{}
		}
		dt := l.At.Sub(r.Seen)
		continuous := dt >= 0 && dt <= s.MaxGap
		if continuous && dt > 0 {
			r.Seconds[r.State] += dt.Seconds()
			for _, u := range r.Uses {
				j := s.job(u.Namespace, u.Job)
				if u.Held {
					j.Held += dt.Seconds()
				} else {
					j.Idle += dt.Seconds()
				}
			}
		}
		if r.State != st || !continuous {
			r.State, r.Since = st, l.At
		}
		r.Node, r.Index, r.Seen = l.Node, e.Index, l.At
		r.Uses = uses(e)
		for _, u := range r.Uses {
			s.job(u.Namespace, u.Job).Seen = l.At
		}
	}
	for uuid, r := range s.GPUs {
		if l.At.Sub(r.Seen) > Retain {
			delete(s.GPUs, uuid)
		}
	}
	for k, j := range s.Jobs {
		if l.At.Sub(j.Seen) > Retain {
			delete(s.Jobs, k)
		}
	}
}

func (s *Store) job(namespace, name string) *Job {
	if s.Jobs == nil {
		s.Jobs = map[string]*Job{}
	}
	k := namespace + "/" + name
	j := s.Jobs[k]
	if j == nil {
		j = &Job{Namespace: namespace, Job: name}
		s.Jobs[k] = j
	}
	return j
}

// uses are the jobs reserving the GPU, each held when its allocation is a reserved
// tenant — the same test fleet uses for a job's held GPUs.
func uses(e ledger.Entry) []Use {
	var out []Use
	seen := map[string]bool{}
	for _, r := range e.Reservations {
		ns := r.Namespace
		if ns == "" {
			ns = "default"
		}
		if seen[ns+"/"+r.JobID] {
			continue
		}
		seen[ns+"/"+r.JobID] = true
		held := false
		for _, t := range e.Tenants {
			if t.AllocID == r.AllocID && t.Reserved {
				held = true
			}
		}
		out = append(out, Use{Namespace: ns, Job: r.JobID, Held: held})
	}
	return out
}

// GPUSeconds is one GPU's seconds in one state.
type GPUSeconds struct {
	Node    string
	Index   int
	UUID    string
	State   ledger.State
	Seconds float64
}

// JobSeconds is one job's GPU-seconds, held and idle.
type JobSeconds struct {
	Namespace, Job string
	Held, Idle     float64
}

// Counters is a snapshot for the metrics, sorted: every state of every GPU (0 when never
// seen, so a rate has a series from the start), and every job.
type Counters struct {
	GPUs []GPUSeconds
	Jobs []JobSeconds
}

var states = []ledger.State{ledger.StateFree, ledger.StateReservedIdle, ledger.StateHeld, ledger.StateUnaccounted}

// Counters returns the snapshot.
func (s *Store) Counters() Counters {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c Counters
	for uuid, r := range s.GPUs {
		for _, st := range states {
			c.GPUs = append(c.GPUs, GPUSeconds{Node: r.Node, Index: r.Index, UUID: uuid, State: st, Seconds: r.Seconds[st]})
		}
	}
	sort.Slice(c.GPUs, func(i, j int) bool {
		a, b := c.GPUs[i], c.GPUs[j]
		if a.Index != b.Index {
			return a.Index < b.Index
		}
		if a.UUID != b.UUID {
			return a.UUID < b.UUID
		}
		return a.State < b.State
	})
	for _, j := range s.Jobs {
		c.Jobs = append(c.Jobs, JobSeconds{Namespace: j.Namespace, Job: j.Job, Held: j.Held, Idle: j.Idle})
	}
	sort.Slice(c.Jobs, func(i, j int) bool {
		if c.Jobs[i].Namespace != c.Jobs[j].Namespace {
			return c.Jobs[i].Namespace < c.Jobs[j].Namespace
		}
		return c.Jobs[i].Job < c.Jobs[j].Job
	})
	return c
}

// Annotate sets StateSince on the entries whose state the history has seen without a
// gap up to the ledger's time.
func (s *Store) Annotate(l *ledger.Ledger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range l.Entries {
		e := &l.Entries[i]
		st := e.State
		if st == "" {
			st = ledger.Classify(*e)
		}
		r := s.GPUs[e.UUID]
		if r == nil || r.State != st || l.At.Sub(r.Seen) > s.MaxGap {
			continue
		}
		since := r.Since
		e.StateSince = &since
	}
}

// Load reads a history written by Save; a missing file is an empty history.
func Load(path string, maxGap time.Duration) (*Store, error) {
	s := New(maxGap)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.GPUs == nil {
		s.GPUs = map[string]*Record{}
	}
	if s.Jobs == nil {
		s.Jobs = map[string]*Job{}
	}
	return s, nil
}

// Save writes the history atomically: a temporary file beside it, then a rename, so
// a reader never sees half a file.
func (s *Store) Save(path string) error {
	s.mu.Lock()
	s.Schema = ledger.Schema
	b, err := json.MarshalIndent(s, "", " ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
