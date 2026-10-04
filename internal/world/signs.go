package world

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"mcgo/internal/mcproto"
)

// SignNBT builds the block entity NBT for a sign. The root compound is unnamed
// (network NBT), matching how block entities travel in both Block Entity Data
// and a chunk's block-entity list. x/y/z are omitted: the packet carries the
// position.
func SignNBT(text SignText) []byte {
	var w mcproto.Writer
	w.WriteByte(0x0A) // TAG_Compound
	writeNBTString(&w, "id", "minecraft:sign")
	writeNBTByte(&w, "is_waxed", 0)
	writeSignSide(&w, "front_text", text.Front)
	writeSignSide(&w, "back_text", text.Back)
	w.WriteByte(0x00) // TAG_End
	return w.Bytes()
}

func writeSignSide(w *mcproto.Writer, name string, lines [4]string) {
	w.WriteByte(0x0A) // TAG_Compound
	writeNBTName(w, name)
	writeNBTString(w, "color", "black")
	writeNBTByte(w, "has_glowing_text", 0)
	// messages: TAG_List (0x09) of TAG_String, one per line. A sign's messages
	// are text *components*; since 1.20 a plain line is a bare string, not
	// JSON. Writing `{"text":"hi"}` here makes the client print exactly that,
	// braces and all.
	//
	// Writing 0x08 as the element type instead of 0x09 makes the client read a
	// String whose length is the next four bytes — 2048 — and the whole
	// compound then misparses.
	w.WriteByte(0x09) // TAG_List
	writeNBTName(w, "messages")
	w.WriteByte(0x08) // element type: TAG_String
	w.WriteInt(4)
	for _, line := range lines {
		w.WriteUShort(uint16(len(line)))
		w.WriteBytes([]byte(line))
	}
	w.WriteByte(0x00) // TAG_End
}

func writeNBTName(w *mcproto.Writer, name string) {
	w.WriteUShort(uint16(len(name)))
	w.WriteBytes([]byte(name))
}

func writeNBTString(w *mcproto.Writer, name, value string) {
	w.WriteByte(0x08)
	writeNBTName(w, name)
	w.WriteUShort(uint16(len(value)))
	w.WriteBytes([]byte(value))
}

func writeNBTByte(w *mcproto.Writer, name string, value byte) {
	w.WriteByte(0x01)
	writeNBTName(w, name)
	w.WriteByte(value)
}

// SignText is the editable text of a sign's two sides. Signs are the first
// block entity: they carry state that lives outside the block palette, so the
// server has to store it and replay it to every client that loads the chunk.
type SignText struct {
	Front [4]string `json:"front"`
	Back  [4]string `json:"back"`
}

// Signs is the shared, persistable record of sign contents, keyed by block
// position. Like Edits, one instance backs every protocol version: a sign
// written by a 26.3 client must read back the same for a 1.21.8 client.
type Signs struct {
	mu    sync.Mutex
	m     map[[3]int32]SignText
	dirty bool
}

type signJSON struct {
	X    int32    `json:"x"`
	Y    int32    `json:"y"`
	Z    int32    `json:"z"`
	Text SignText `json:"text"`
}

type signsFile struct {
	Version int        `json:"version"`
	Signs   []signJSON `json:"signs"`
}

const signsFormatVersion = 1

func NewSigns() *Signs {
	return &Signs{m: make(map[[3]int32]SignText)}
}

// Count reports how many signs are stored.
func (s *Signs) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// Get returns the text at a position, if a sign is recorded there.
func (s *Signs) Get(x, y, z int32) (SignText, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[[3]int32{x, y, z}]
	return t, ok
}

// Put records text at a position.
func (s *Signs) Put(x, y, z int32, t SignText) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := [3]int32{x, y, z}
	if old, ok := s.m[key]; ok && old == t {
		return
	}
	s.m[key] = t
	s.dirty = true
}

// Delete forgets a sign's text, for when the block is broken or replaced. A
// store entry that outlives its block means a sign placed there later inherits
// the old text, and a chunk lists a block entity for a block that is gone.
func (s *Signs) Delete(x, y, z int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := [3]int32{x, y, z}
	if _, ok := s.m[key]; !ok {
		return
	}
	delete(s.m, key)
	s.dirty = true
}

// InChunk returns the signs inside chunk (cx, cz).
func (s *Signs) InChunk(cx, cz int32) map[[3]int32]SignText {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[[3]int32]SignText)
	for k, v := range s.m {
		if k[0]>>4 == cx && k[2]>>4 == cz {
			out[k] = v
		}
	}
	return out
}

// Load reads signs from path. A missing file is not an error.
func (s *Signs) Load(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f signsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("signs %s: %w", path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range f.Signs {
		s.m[[3]int32{e.X, e.Y, e.Z}] = e.Text
	}
	return nil
}

// Save writes signs atomically, and only when something changed.
func (s *Signs) Save(path string) error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	f := signsFile{Version: signsFormatVersion, Signs: make([]signJSON, 0, len(s.m))}
	for k, v := range s.m {
		f.Signs = append(f.Signs, signJSON{X: k[0], Y: k[1], Z: k[2], Text: v})
	}
	s.dirty = false
	s.mu.Unlock()

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
