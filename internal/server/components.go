package server

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"mcgo/internal/mcproto"
	"mcgo/internal/nbt"
	"mcgo/internal/world"
)

// creativeComponents reads the component patch of a creative-mode slot.
//
// The creative-inventory packet carries components in the *delimited* form:
// each value is length-prefixed, which is exactly what makes it possible to
// read every component without implementing a codec per type. An unknown
// component is skipped by its length and kept verbatim.
func creativeComponents(r *mcproto.Reader) ([]world.Component, error) {
	added, err := r.ReadVarInt()
	if err != nil {
		return nil, err
	}
	removed, err := r.ReadVarInt()
	if err != nil {
		return nil, err
	}
	if added < 0 || removed < 0 || added > 256 || removed > 256 {
		return nil, fmt.Errorf("component patch with %d added, %d removed", added, removed)
	}
	out := make([]world.Component, 0, added+removed)
	for range added {
		typ, err := r.ReadVarInt()
		if err != nil {
			return nil, err
		}
		size, err := r.ReadVarInt()
		if err != nil {
			return nil, err
		}
		value, err := r.ReadBytes(int(size))
		if err != nil {
			return nil, err
		}
		out = append(out, world.Component{Type: typ, Value: value})
	}
	for range removed {
		typ, err := r.ReadVarInt()
		if err != nil {
			return nil, err
		}
		out = append(out, world.Component{Type: typ, Removed: true})
	}
	return out, nil
}

// textComponentName pulls the plain text out of a network-NBT text component.
// A component is either a bare string or a compound with a "text" field;
// anything richer (a translation key, a list of siblings) is left alone, and
// the name simply is not preserved.
func textComponentName(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case 0x08: // TAG_String: a bare name
		if len(b) < 3 {
			return ""
		}
		n := int(binary.BigEndian.Uint16(b[1:3]))
		if len(b) < 3+n {
			return ""
		}
		return string(b[3 : 3+n])
	case 0x0A: // TAG_Compound: {"text": "..."}
		tag, err := nbt.ReadCompound(bytes.NewReader(b[1:]))
		if err != nil {
			return ""
		}
		if text, ok := tag.Get("text"); ok && text.Type == nbt.String {
			return text.Str
		}
	}
	return ""
}
