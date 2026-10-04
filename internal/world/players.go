package world

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PlayerState is the persistable slice of a session: where the player was and
// what they were holding. Inventory entries are minecraft:item registry IDs —
// the same space Set Creative Mode Slot carries — so they can be replayed to
// the client on reconnect without translating through block states.
type PlayerState struct {
	Name  string  `json:"name"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Yaw   float32 `json:"yaw"`
	Pitch float32 `json:"pitch"`
	// Inv is the player's 36 storage slots in window order: indices 0-26 are
	// the main inventory (window slots 9-35) and 27-35 are the hotbar
	// (window slots 36-44).
	Inv      [36]Item `json:"inv"`
	HeldSlot int32    `json:"heldSlot"`
	HasPos   bool     `json:"hasPos"`
}

// Players is the shared, persistable record of per-player state, keyed by the
// dashed UUID string.
type Players struct {
	mu    sync.Mutex
	m     map[string]PlayerState
	dirty bool
}

type playersFile struct {
	Version int                    `json:"version"`
	Players map[string]PlayerState `json:"players"`
}

const playersFormatVersion = 1

func NewPlayers() *Players {
	return &Players{m: make(map[string]PlayerState)}
}

// Count reports how many players have saved state.
func (p *Players) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.m)
}

// Get returns the stored state for key, if any.
func (p *Players) Get(key string) (PlayerState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.m[key]
	return st, ok
}

// Put stores state for key, marking the store dirty when it changed.
func (p *Players) Put(key string, st PlayerState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if old, ok := p.m[key]; ok && sameState(old, st) {
		return
	}
	p.m[key] = st
	p.dirty = true
}

// sameState compares two states. It cannot be == any more: an item carries a
// component slice, so the array of items is no longer comparable.
func sameState(a, b PlayerState) bool {
	if a.Name != b.Name || a.X != b.X || a.Y != b.Y || a.Z != b.Z ||
		a.Yaw != b.Yaw || a.Pitch != b.Pitch || a.HeldSlot != b.HeldSlot || a.HasPos != b.HasPos {
		return false
	}
	for i := range a.Inv {
		if !a.Inv[i].Same(b.Inv[i]) || a.Inv[i].Count != b.Inv[i].Count {
			return false
		}
	}
	return true
}

// Load reads player state from path. A missing file is not an error.
func (p *Players) Load(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f playersFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("players %s: %w", path, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range f.Players {
		p.m[k] = v
	}
	return nil
}

// Save writes player state atomically, and only when something changed.
func (p *Players) Save(path string) error {
	p.mu.Lock()
	if !p.dirty {
		p.mu.Unlock()
		return nil
	}
	f := playersFile{Version: playersFormatVersion, Players: make(map[string]PlayerState, len(p.m))}
	for k, v := range p.m {
		f.Players[k] = v
	}
	p.dirty = false
	p.mu.Unlock()

	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Clean(path))
}
