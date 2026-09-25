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
	"sync"
	"time"

	"github.com/hiway-media/gpuledger/internal/ledger"
)

// Retain is how long a GPU no longer on the node is remembered.
const Retain = 7 * 24 * time.Hour

// Record is one GPU's state and its times. Nothing about tenants is kept.
type Record struct {
	Node  string       `json:"node"`
	State ledger.State `json:"state"`
	Since time.Time    `json:"since"`
	Seen  time.Time    `json:"seen"`
}

// Store is the history, by GPU UUID. MaxGap is the longest silence between two
// observations still read as continuous — three refresh intervals for serve.
type Store struct {
	mu     sync.Mutex
	GPUs   map[string]*Record `json:"gpus"`
	MaxGap time.Duration      `json:"-"`
}

// New is an empty history.
func New(maxGap time.Duration) *Store {
	return &Store{GPUs: map[string]*Record{}, MaxGap: maxGap}
}

// Observe records a ledger. A partial ledger — a source failed — is not an
// observation: its states may be wrong, and the time it covers becomes a gap.
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
		if r == nil || r.State != st || l.At.Sub(r.Seen) > s.MaxGap {
			r = &Record{Node: l.Node, State: st, Since: l.At}
			s.GPUs[e.UUID] = r
		}
		r.Seen = l.At
	}
	for uuid, r := range s.GPUs {
		if l.At.Sub(r.Seen) > Retain {
			delete(s.GPUs, uuid)
		}
	}
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
	return s, nil
}

// Save writes the history atomically: a temporary file beside it, then a rename, so
// a reader never sees half a file.
func (s *Store) Save(path string) error {
	s.mu.Lock()
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
