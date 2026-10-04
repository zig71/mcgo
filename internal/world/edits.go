package world

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Edits is the shared, persistable record of block changes. Every protocol
// version's World references the same Edits, because they describe one world:
// a block broken by a 26.3 client must be gone for a 1.21.8 client too.
type Edits struct {
	mu    sync.Mutex
	m     map[[3]int32]int32
	dirty bool
}

type editJSON struct {
	X  int32 `json:"x"`
	Y  int32 `json:"y"`
	Z  int32 `json:"z"`
	ID int32 `json:"id"`
}

type editsFile struct {
	Version int        `json:"version"`
	Edits   []editJSON `json:"edits"`
}

const editsFormatVersion = 1

func NewEdits() *Edits {
	return &Edits{m: make(map[[3]int32]int32)}
}

// Each calls fn for every recorded edit. The world is walked at startup to find
// fluid that was flowing when the server stopped, so it can flow again.
func (e *Edits) Each(fn func(pos [3]int32, id int32)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for p, id := range e.m {
		fn(p, id)
	}
}

// Count reports how many edits are recorded.
func (e *Edits) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.m)
}

// Load reads edits from path. A missing file is not an error: it just means a
// fresh world.
func (e *Edits) Load(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f editsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("world file %s: %w", path, err)
	}
	if f.Version != editsFormatVersion {
		return fmt.Errorf("world file %s: unsupported version %d", path, f.Version)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ed := range f.Edits {
		e.m[[3]int32{ed.X, ed.Y, ed.Z}] = ed.ID
	}
	return nil
}

// Save writes edits atomically, and only when something changed.
func (e *Edits) Save(path string) error {
	e.mu.Lock()
	if !e.dirty {
		e.mu.Unlock()
		return nil
	}
	f := editsFile{Version: editsFormatVersion, Edits: make([]editJSON, 0, len(e.m))}
	for p, id := range e.m {
		f.Edits = append(f.Edits, editJSON{X: p[0], Y: p[1], Z: p[2], ID: id})
	}
	e.dirty = false
	e.mu.Unlock()

	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EnsureDir creates the parent directory of path if needed.
func EnsureDir(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		return os.MkdirAll(dir, 0o755)
	}
	return nil
}
