package world

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"mcgo/internal/nbt"
)

// buildNamedTag emits a named compound containing {Level: int, Name: string}.
func buildNamedTag(root string) []byte {
	var b bytes.Buffer
	b.WriteByte(0x0A) // TAG_Compound
	binary.Write(&b, binary.BigEndian, uint16(len(root)))
	b.WriteString(root)

	b.WriteByte(0x03) // TAG_Int
	binary.Write(&b, binary.BigEndian, uint16(len("Level")))
	b.WriteString("Level")
	binary.Write(&b, binary.BigEndian, int32(42))

	b.WriteByte(0x08) // TAG_String
	binary.Write(&b, binary.BigEndian, uint16(len("Name")))
	b.WriteString("Name")
	binary.Write(&b, binary.BigEndian, uint16(2))
	b.WriteString("hi")

	b.WriteByte(0x00) // TAG_End
	return b.Bytes()
}

// writeRegion builds a one-chunk region file holding a zlib-compressed tag.
func writeRegion(t *testing.T, path string, tag []byte) {
	t.Helper()

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(tag); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// Sector 0-1: the 8 KiB index. Chunk (0,0) is index 0.
	const dataSector = 2
	body := make([]byte, 5+compressed.Len())
	binary.BigEndian.PutUint32(body[:4], uint32(1+compressed.Len()))
	body[4] = 2 // zlib
	copy(body[5:], compressed.Bytes())

	sectors := (len(body) + 4095) / 4096
	index := make([]byte, 4096)
	index[0] = byte(dataSector >> 16)
	index[1] = byte(dataSector >> 8)
	index[2] = byte(dataSector)
	index[3] = byte(sectors)

	file := append(index, make([]byte, 4096)...) // index + timestamps
	file = append(file, body...)
	file = append(file, make([]byte, sectors*4096-len(body))...)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, file, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegionReadChunk(t *testing.T) {
	dir := t.TempDir()
	path := RegionPath(dir, 0, 0)
	if got, want := filepath.Base(path), "r.0.0.mca"; got != want {
		t.Fatalf("RegionPath base = %q, want %q", got, want)
	}

	writeRegion(t, path, buildNamedTag("root"))

	r, err := OpenRegion(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	raw, ok, err := r.Chunk(0, 0)
	if err != nil || !ok {
		t.Fatalf("Chunk(0,0) = %v, %v", ok, err)
	}

	name, tag, err := nbt.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if name != "root" {
		t.Errorf("root name = %q, want %q", name, "root")
	}
	if v, _ := tag.Get("Level"); v.Int() != 42 {
		t.Errorf("Level = %d, want 42", v.Int())
	}
	if v, _ := tag.Get("Name"); v.Str != "hi" {
		t.Errorf("Name = %q, want %q", v.Str, "hi")
	}

	// A chunk the region never generated reads as absent, not an error.
	if _, ok, err := r.Chunk(5, 7); err != nil || ok {
		t.Errorf("Chunk(5,7) = %v, %v; want absent", ok, err)
	}
}
