package world

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"mcgo/internal/mcproto"
)

// Banners is the pattern layers on the banners players have placed.
//
// A banner's patterns are not in its block state — they arrive as the
// minecraft:banner_patterns data component of the item that was placed, and the
// block entity has to carry them or the banner renders plain. The value is kept
// exactly as the item carried it: the component is BannerPatternLayers and the
// block entity's patterns field is BannerPatternLayers too, so its bytes ARE
// the field's bytes and re-encoding them could only get it wrong.
type Banners struct {
	mu    sync.Mutex
	m     map[[3]int32][]byte
	dirty bool
}

type bannersFile struct {
	Version int          `json:"version"`
	Banners []bannerJSON `json:"banners"`
}

type bannerJSON struct {
	X        int32  `json:"x"`
	Y        int32  `json:"y"`
	Z        int32  `json:"z"`
	Patterns []byte `json:"patterns"`
}

const bannersFormatVersion = 1

func NewBanners() *Banners {
	return &Banners{m: make(map[[3]int32][]byte)}
}

// Count reports how many banners are stored.
func (b *Banners) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.m)
}

// Get returns the pattern layers at a position, if a banner is recorded there.
func (b *Banners) Get(x, y, z int32) ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[[3]int32{x, y, z}]
	return v, ok
}

// Put records pattern layers at a position.
func (b *Banners) Put(x, y, z int32, patterns []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m[[3]int32{x, y, z}] = patterns
	b.dirty = true
}

// Delete forgets a banner, for when the block is broken or replaced.
func (b *Banners) Delete(x, y, z int32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := [3]int32{x, y, z}
	if _, ok := b.m[key]; !ok {
		return
	}
	delete(b.m, key)
	b.dirty = true
}

// InChunk returns the banners inside chunk (cx, cz).
func (b *Banners) InChunk(cx, cz int32) map[[3]int32][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[[3]int32][]byte)
	for k, v := range b.m {
		if k[0]>>4 == cx && k[2]>>4 == cz {
			out[k] = v
		}
	}
	return out
}

// Load reads banners from path. A missing file is not an error.
func (b *Banners) Load(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f bannersFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("banners %s: %w", path, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range f.Banners {
		b.m[[3]int32{e.X, e.Y, e.Z}] = e.Patterns
	}
	return nil
}

// Save writes banners atomically, and only when something changed.
func (b *Banners) Save(path string) error {
	b.mu.Lock()
	if !b.dirty {
		b.mu.Unlock()
		return nil
	}
	f := bannersFile{Version: bannersFormatVersion, Banners: make([]bannerJSON, 0, len(b.m))}
	for k, v := range b.m {
		f.Banners = append(f.Banners, bannerJSON{X: k[0], Y: k[1], Z: k[2], Patterns: v})
	}
	b.dirty = false
	b.mu.Unlock()
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	return os.Rename(tmp, path)
}

// BannerNBT is the block entity NBT for a banner: its id, and the pattern
// layers when it has any.
//
// The patterns field is omitted when there are none, which is what vanilla
// does — a fresh banner's block entity carries only its id.
func BannerNBT(patterns []byte) []byte {
	var w mcproto.Writer
	w.WriteByte(0x0A) // TAG_Compound
	writeNBTString(&w, "id", "minecraft:banner")
	if count := patternCount(patterns); count > 0 {
		// The component's value is a TAG_List: its type byte, then the list
		// payload (element type, length, elements). The block entity wants the
		// same list under the name "patterns".
		w.WriteByte(0x09)
		writeNBTName(&w, "patterns")
		w.WriteBytes(patterns[1:])
	}
	w.WriteByte(0x00) // TAG_End
	return w.Bytes()
}

// patternCount reports how many layers a component value holds, 0 when the
// value is not a list.
func patternCount(value []byte) int {
	// TAG_List: type byte, element type, then a big-endian int length.
	if len(value) < 5 || value[0] != 0x09 {
		return 0
	}
	n := int(value[2])<<24 | int(value[3])<<16 | int(value[4])<<8 | int(value[5])
	return n
}
