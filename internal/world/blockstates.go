package world

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// BlockStateTable resolves Anvil palette entries — a block name plus
// properties — to numeric block-state ids, which is what the network format
// needs.
//
// The generated table holds ~35k states. Building a map from them would cost
// megabytes of heap for no reason: the blob is already sorted, so lookups
// binary-search it through a small offset index instead.
type BlockStateTable struct {
	blob     []byte
	offsets  []uint32 // start of each key within blob
	lengths  []uint16 // key length; keys are length-prefixed, so this is cached
	ids      []int32  // block state id per key
	defaults map[string]string
}

// CanonKey builds the lookup key for a state: name plus properties in sorted
// order. The generator uses exactly this form.
func CanonKey(name string, props map[string]string) string {
	if len(props) == 0 {
		return name
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('[')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(props[k])
	}
	b.WriteByte(']')
	return b.String()
}

// LoadBlockStates indexes the generated blob without copying its strings.
func LoadBlockStates(blob []byte) (*BlockStateTable, error) {
	t := &BlockStateTable{blob: blob, defaults: make(map[string]string, 2048)}
	pos := 0

	if pos+4 > len(blob) {
		return nil, fmt.Errorf("blockstates: missing state count")
	}
	n := int(binary.BigEndian.Uint32(blob[pos:]))
	pos += 4
	t.offsets = make([]uint32, 0, n)
	t.lengths = make([]uint16, 0, n)
	t.ids = make([]int32, 0, n)

	for range n {
		if pos+4 > len(blob) {
			return nil, fmt.Errorf("blockstates: truncated state id")
		}
		id := int32(binary.BigEndian.Uint32(blob[pos:]))
		pos += 4
		if pos+2 > len(blob) {
			return nil, fmt.Errorf("blockstates: truncated key length")
		}
		length := binary.BigEndian.Uint16(blob[pos:])
		pos += 2
		if pos+int(length) > len(blob) {
			return nil, fmt.Errorf("blockstates: truncated key")
		}
		t.offsets = append(t.offsets, uint32(pos))
		t.lengths = append(t.lengths, length)
		t.ids = append(t.ids, id)
		pos += int(length)
	}

	if pos+4 > len(blob) {
		return nil, fmt.Errorf("blockstates: missing block count")
	}
	n = int(binary.BigEndian.Uint32(blob[pos:]))
	pos += 4
	for range n {
		name, next, err := readU16(blob, pos)
		if err != nil {
			return nil, err
		}
		props, end, err := readU16(blob, next)
		if err != nil {
			return nil, err
		}
		t.defaults[name] = props
		pos = end
	}
	return t, nil
}

// readU16 reads a length-prefixed string and returns it with the offset just
// past the value.
func readU16(b []byte, pos int) (string, int, error) {
	if pos+2 > len(b) {
		return "", 0, fmt.Errorf("blockstates: truncated length")
	}
	n := int(binary.BigEndian.Uint16(b[pos:]))
	pos += 2
	if pos+n > len(b) {
		return "", 0, fmt.Errorf("blockstates: truncated value")
	}
	return string(b[pos : pos+n]), pos + n, nil
}

// NameOf resolves a state id back to its block name and properties. Placement
// needs it to look at a neighbouring block: whether the chest beside this one
// is a chest, and which way it faces.
func (t *BlockStateTable) NameOf(id int32) (string, map[string]string, bool) {
	for i, got := range t.ids {
		if got != id {
			continue
		}
		name, props := ParseStateString(string(t.key(i)))
		return name, props, true
	}
	return "", nil, false
}

// key returns the i-th key without allocating.
func (t *BlockStateTable) key(i int) []byte {
	off := t.offsets[i]
	return t.blob[off : off+uint32(t.lengths[i])]
}

// Lookup resolves a palette entry. A palette may carry only the properties
// that differ from the default, so a partial match is completed from the
// block's default property list and retried.
func (t *BlockStateTable) Lookup(name string, props map[string]string) (int32, bool) {
	if id, ok := t.find(CanonKey(name, props)); ok {
		return id, true
	}
	defStr, ok := t.defaults[name]
	if !ok {
		return 0, false
	}
	merged := parseCanon(defStr)
	for k, v := range props {
		merged[k] = v
	}
	if id, ok := t.find(CanonKey(name, merged)); ok {
		return id, true
	}
	return 0, false
}

// find binary-searches the sorted key table.
func (t *BlockStateTable) find(key string) (int32, bool) {
	want := []byte(key)
	i := sort.Search(len(t.offsets), func(i int) bool {
		return bytes.Compare(t.key(i), want) >= 0
	})
	if i < len(t.offsets) && bytes.Equal(t.key(i), want) {
		return t.ids[i], true
	}
	return 0, false
}

// ParseStateString splits a saved block state string such as
// "minecraft:oak_stairs[facing=east,half=bottom]" into a name and the
// properties it lists.
func ParseStateString(s string) (string, map[string]string) {
	open := strings.IndexByte(s, '[')
	if open < 0 || !strings.HasSuffix(s, "]") {
		return s, nil
	}
	props := map[string]string{}
	for _, kv := range strings.Split(s[open+1:len(s)-1], ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			props[k] = v
		}
	}
	return s[:open], props
}

// parseCanon turns "[a=b,c=d]" back into a map.
func parseCanon(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	if s == "" {
		return out
	}
	for _, kv := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}
