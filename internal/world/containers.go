package world

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Component is one data component of an item: its registry id and its
// already-encoded value.
//
// Values are kept as raw bytes: the server never has to understand most
// components, only carry them. That is what makes a written book, a banner
// pattern or an enchanted item survive a trip through a chest or the creative
// inventory without an encoder for every component in the game.
type Component struct {
	Type  int32  `json:"type"`
	Value []byte `json:"value,omitempty"`
	// Removed marks a component the patch takes away rather than sets.
	Removed bool `json:"removed,omitempty"`
}

// Item is one inventory slot: a minecraft:item registry ID, a stack size, and
// the data components the item carries.
//
// Count 0 means the slot is empty, which is how the wire format expresses it.
type Item struct {
	ID         int32       `json:"id"`
	Count      int32       `json:"count"`
	Components []Component `json:"components,omitempty"`
}

// Empty reports whether the slot holds nothing.
func (i Item) Empty() bool { return i.Count <= 0 || i.ID == 0 }

// Component returns the item's value for a component type.
func (i Item) Component(typ int32) ([]byte, bool) {
	for _, c := range i.Components {
		if c.Type == typ && !c.Removed {
			return c.Value, true
		}
	}
	return nil, false
}

// SetComponent replaces a component's value, dropping any removal marker for
// the same type.
func (i *Item) SetComponent(typ int32, value []byte) {
	i.RemoveComponent(typ)
	i.Components = append(i.Components, Component{Type: typ, Value: value})
}

// RemoveComponent takes a component off the item.
func (i *Item) RemoveComponent(typ int32) {
	kept := i.Components[:0]
	for _, c := range i.Components {
		if c.Type != typ {
			kept = append(kept, c)
		}
	}
	i.Components = kept
}

// Same reports whether two slots hold the same thing, which is what stacking
// and merging need: a renamed sword must not merge into a plain one, and two
// different enchanted books must not either.
func (i Item) Same(other Item) bool {
	if i.ID != other.ID || len(i.Components) != len(other.Components) {
		return false
	}
	for _, c := range i.Components {
		value, ok := other.Component(c.Type)
		if !ok || !bytes.Equal(value, c.Value) {
			return false
		}
	}
	return true
}

// FurnaceState is the cooking state of one furnace, in the same four values
// vanilla exposes as container data properties: fuel left and its total, and
// cooking progress and its total.
type FurnaceState struct {
	Lit       int32 `json:"lit,omitempty"`
	LitTotal  int32 `json:"litTotal,omitempty"`
	Cook      int32 `json:"cook,omitempty"`
	CookTotal int32 `json:"cookTotal,omitempty"`
}

// entry is one container's contents. Furnaces also carry cooking state, which
// is why the store is keyed by position rather than being a plain item map.
type entry struct {
	items   []Item
	furnace FurnaceState
	hasFurn bool
}

// Containers is the shared, persistable record of container contents (chests,
// barrels, hoppers, shulker boxes, furnaces), keyed by block position.
type Containers struct {
	mu    sync.Mutex
	m     map[[3]int32]*entry
	dirty bool
}

type containerJSON struct {
	X       int32         `json:"x"`
	Y       int32         `json:"y"`
	Z       int32         `json:"z"`
	Items   []Item        `json:"items"`
	Furnace *FurnaceState `json:"furnace,omitempty"`
}

type containersFile struct {
	Version    int             `json:"version"`
	Containers []containerJSON `json:"containers"`
}

const containersFormatVersion = 1

func NewContainers() *Containers {
	return &Containers{m: make(map[[3]int32]*entry)}
}

// Count reports how many containers hold something.
func (c *Containers) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// Get returns the contents at a position, creating an empty container of the
// given size if none is recorded. A recorded container whose size differs (the
// block was replaced by a different container) is resized, keeping what fits.
func (c *Containers) Get(x, y, z int32, size int) []Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(x, y, z)
	if len(e.items) != size {
		resized := make([]Item, size)
		copy(resized, e.items)
		e.items = resized
		c.dirty = true
	}
	out := make([]Item, len(e.items))
	copy(out, e.items)
	return out
}

// Put stores contents at a position.
func (c *Containers) Put(x, y, z int32, items []Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(x, y, z)
	stored := make([]Item, len(items))
	copy(stored, items)
	e.items = stored
	c.dirty = true
}

// Furnace returns a furnace's contents and cooking state.
func (c *Containers) Furnace(x, y, z int32) ([]Item, FurnaceState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(x, y, z)
	items := make([]Item, len(e.items))
	copy(items, e.items)
	return items, e.furnace
}

// PutFurnace stores a furnace's contents and cooking state.
func (c *Containers) PutFurnace(x, y, z int32, items []Item, f FurnaceState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(x, y, z)
	stored := make([]Item, len(items))
	copy(stored, items)
	e.items = stored
	e.furnace = f
	e.hasFurn = true
	c.dirty = true
}

// MarkFurnace records that a position holds a furnace, so the tick loop knows
// to cook in it even before anyone puts fuel in.
func (c *Containers) MarkFurnace(x, y, z int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entryLocked(x, y, z).hasFurn = true
	c.dirty = true
}

// Remove drops any contents recorded at a position, which is what breaking a
// container block does.
func (c *Containers) Remove(x, y, z int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[[3]int32{x, y, z}]; ok {
		delete(c.m, [3]int32{x, y, z})
		c.dirty = true
	}
}

// FurnaceTick is one furnace the cooking loop should advance.
type FurnaceTick struct {
	Pos  [3]int32
	Furn FurnaceState
}

// Furnaces lists the positions that hold furnace state, for the cooking tick.
func (c *Containers) Furnaces() []FurnaceTick {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]FurnaceTick, 0, len(c.m))
	for k, e := range c.m {
		if e.hasFurn {
			out = append(out, FurnaceTick{Pos: k, Furn: e.furnace})
		}
	}
	return out
}

func (c *Containers) entryLocked(x, y, z int32) *entry {
	key := [3]int32{x, y, z}
	e, ok := c.m[key]
	if !ok {
		e = &entry{}
		c.m[key] = e
	}
	return e
}

// Load reads containers from path. A missing file is not an error.
func (c *Containers) Load(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f containersFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("containers %s: %w", path, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range f.Containers {
		items := make([]Item, len(e.Items))
		copy(items, e.Items)
		ent := &entry{items: items}
		if e.Furnace != nil {
			ent.furnace = *e.Furnace
			ent.hasFurn = true
		}
		c.m[[3]int32{e.X, e.Y, e.Z}] = ent
	}
	return nil
}

// Save writes containers atomically, and only when something changed.
func (c *Containers) Save(path string) error {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return nil
	}
	f := containersFile{Version: containersFormatVersion, Containers: make([]containerJSON, 0, len(c.m))}
	for k, v := range c.m {
		// Skip containers that were opened but never filled and hold no
		// cooking state.
		if !v.hasFurn || v.furnace == (FurnaceState{}) {
			empty := true
			for _, it := range v.items {
				if !it.Empty() {
					empty = false
					break
				}
			}
			if empty {
				continue
			}
		}
		e := containerJSON{X: k[0], Y: k[1], Z: k[2], Items: v.items}
		if v.hasFurn {
			st := v.furnace
			e.Furnace = &st
		}
		f.Containers = append(f.Containers, e)
	}
	c.dirty = false
	c.mu.Unlock()

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
