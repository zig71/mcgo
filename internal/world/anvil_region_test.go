package world

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"mcgo/internal/nbt"
	"mcgo/internal/proto/v776"
)

// These tests run against a chunk written by the vanilla server, which is the
// only oracle that matters for the Anvil side: bytes this project did not
// write. They skip when no world is present, so a normal `go test ./...` stays
// self-contained.
//
// Point MCGO_WORLD at a world directory (the one containing `region/`) to run
// them, or let a generated world sit at the default path.
func worldDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("MCGO_WORLD")
	if dir == "" {
		dir = "/home/ubuntu/tmp/genworld/world"
	}
	if st, err := os.Stat(filepath.Join(dir, "region")); err != nil || !st.IsDir() {
		t.Skipf("no world at %s; set MCGO_WORLD to run", dir)
	}
	return dir
}

func loadCtx(t *testing.T) ChunkContext {
	t.Helper()
	blob, err := os.ReadFile("../../internal/proto/v776/blockstates.bin")
	if err != nil {
		t.Fatal(err)
	}
	table, err := LoadBlockStates(blob)
	if err != nil {
		t.Fatal(err)
	}
	return ChunkContext{
		Table: table,
		// Ids here only have to be consistent: the client's registry ids come
		// from the registry data the server sends, which is a separate concern.
		Biomes:           map[string]int32{"minecraft:plains": 1},
		BlockEntityTypes: v776.BlockEntityTypes,
		MinY:             -64,
		Sections:         24,
	}
}

// TestConvertRealChunk converts real chunks and decodes the result the way the
// client does, then compares every block state against the source NBT. The
// failure this guards against is a packing mismatch — right data, wrong bits —
// which no amount of self-consistent round-tripping would catch.
func TestConvertRealChunk(t *testing.T) {
	dir := worldDir(t)
	ctx := loadCtx(t)

	// Prefer chunks that actually have terrain: an untouched chunk is 24
	// sections of air and would pass without exercising anything.
	var cands []chunkCand
	for _, rc := range [][2]int32{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}} {
		reg, err := OpenRegion(RegionPath(dir, rc[0]*32, rc[1]*32))
		if err != nil {
			continue
		}
		for cx := rc[0] * 32; cx < rc[0]*32+32; cx++ {
			for cz := rc[1] * 32; cz < rc[1]*32+32; cz++ {
				raw, ok, err := reg.Chunk(cx, cz)
				if err != nil {
					t.Fatalf("read chunk (%d,%d): %v", cx, cz, err)
				}
				if ok {
					cands = append(cands, chunkCand{cx, cz, raw, richness(raw)})
				}
			}
		}
		reg.Close()
	}
	if len(cands) == 0 {
		t.Fatal("no chunks found in any region file")
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].rich > cands[j].rich })
	if cands[0].rich == 0 {
		t.Fatal("every chunk is untouched air; cannot validate the palette path")
	}
	// Ids here only have to be consistent: the client's registry ids come from
	// the registry data the server sends, which is a separate concern.
	ctx.Biomes = biomeMap(cands)

	checked := 0
	for _, c := range cands {
		if checked >= 4 {
			break
		}
		body, err := ConvertChunk(c.raw, c.cx, c.cz, ctx)
		if err != nil {
			t.Fatalf("convert chunk (%d,%d): %v", c.cx, c.cz, err)
		}
		if err := compareChunk(body, c.raw, ctx); err != nil {
			t.Errorf("chunk (%d,%d): %v", c.cx, c.cz, err)
		}
		checked++
	}
	t.Logf("verified %d chunks from %s (richest has %d populated sections)", checked, dir, cands[0].rich)
}

// chunkCand is a chunk read from a region file plus how much terrain it holds.
type chunkCand struct {
	cx, cz int32
	raw    []byte
	rich   int
}

// biomeMap assigns an id to every biome the world uses, in sorted order. The
// ids are only required to be self-consistent for this test: the ids that
// matter to a client come from the registry data the server sends.
func biomeMap(cands []chunkCand) map[string]int32 {
	names := map[string]bool{}
	for _, c := range cands {
		_, root, err := nbt.Read(bytes.NewReader(c.raw))
		if err != nil {
			continue
		}
		sections, ok := root.Get("sections")
		if !ok {
			continue
		}
		for _, s := range sections.List {
			b, ok := s.Get("biomes")
			if !ok {
				continue
			}
			p, ok := b.Get("palette")
			if !ok {
				continue
			}
			for _, e := range p.List {
				if e.Type == nbt.String {
					names[e.Str] = true
				}
			}
		}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	out := make(map[string]int32, len(sorted))
	for i, n := range sorted {
		out[n] = int32(i)
	}
	return out
}

// richness counts sections that carry packed block data, which is the signal
// that a chunk has terrain rather than being untouched air.
func richness(raw []byte) int {
	_, root, err := nbt.Read(bytes.NewReader(raw))
	if err != nil {
		return 0
	}
	sections, ok := root.Get("sections")
	if !ok {
		return 0
	}
	n := 0
	for _, s := range sections.List {
		bs, ok := s.Get("block_states")
		if !ok {
			continue
		}
		if _, ok := bs.Get("data"); ok {
			n++
		}
		if b, ok := s.Get("biomes"); ok {
			if _, ok := b.Get("data"); ok {
				n++
			}
		}
	}
	return n
}

// compareChunk decodes the converter's output and the source NBT independently
// and checks that they agree block for block, plus the structural invariants
// the client's reader relies on.
func compareChunk(body, raw []byte, ctx ChunkContext) error {
	_, root, err := nbt.Read(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("source NBT: %w", err)
	}

	src := map[int][]int32{}
	if sections, ok := root.Get("sections"); ok {
		for _, s := range sections.List {
			y, ok := s.Get("Y")
			if !ok {
				continue
			}
			bs, ok := s.Get("block_states")
			if !ok {
				continue // a section with no container is entirely air
			}
			vals, err := blockValues(bs, ctx)
			if err != nil {
				return fmt.Errorf("section %d: %w", y.Int(), err)
			}
			src[int(y.Int())] = vals
		}
	}

	r := &byteReader{b: body}
	if err := r.skip(8); err != nil { // chunk x and z
		return err
	}
	// Heightmaps: a prefixed map of enum id -> long array.
	hmCount, err := r.varint()
	if err != nil {
		return err
	}
	if hmCount < 0 || hmCount > 8 {
		return fmt.Errorf("heightmap count %d", hmCount)
	}
	for range hmCount {
		if _, err := r.varint(); err != nil {
			return err
		}
		longs, err := r.varint()
		if err != nil {
			return err
		}
		if err := r.skip(int(longs) * 8); err != nil {
			return err
		}
	}
	// The section buffer is length-prefixed.
	cdLen, err := r.varint()
	if err != nil {
		return err
	}
	if cdLen <= 0 || r.remaining() < int(cdLen) {
		return fmt.Errorf("section buffer length %d with %d bytes left", cdLen, r.remaining())
	}
	sec := &byteReader{b: body[r.i : r.i+int(cdLen)]}
	r.i += int(cdLen)

	for i := range ctx.Sections {
		nonEmpty, err := sec.u16()
		if err != nil {
			return err
		}
		if _, err := sec.u16(); err != nil { // fluid count
			return err
		}
		got, err := sec.paletted(4096, 8)
		if err != nil {
			return fmt.Errorf("section %d blocks: %w", i, err)
		}
		if _, err := sec.paletted(64, 3); err != nil {
			return fmt.Errorf("section %d biomes: %w", i, err)
		}

		y := ctx.MinY/16 + i
		want, present := src[y]
		if !present {
			want = make([]int32, 4096) // absent from the file means all air
		}
		diff := 0
		for j := range want {
			if want[j] != got[j] {
				diff++
			}
		}
		if diff != 0 {
			return fmt.Errorf("section %d (y=%d): %d of 4096 blocks differ, first at %d: %d != %d",
				i, y, diff, firstDiff(want, got), want[firstDiff(want, got)], got[firstDiff(want, got)])
		}
		// nonEmptyBlockCount must match the states we just wrote: the client
		// uses it to skip empty sections.
		wantNonEmpty := 0
		for _, v := range got {
			if v != 0 {
				wantNonEmpty++
			}
		}
		if int(nonEmpty) != wantNonEmpty {
			return fmt.Errorf("section %d: nonEmptyBlockCount %d, actual %d", i, nonEmpty, wantNonEmpty)
		}
	}

	if sec.remaining() != 0 {
		return fmt.Errorf("%d bytes left in the section buffer", sec.remaining())
	}

	// Block entities come before the light: they are part of the chunk data,
	// while the light is a separate field of the packet.
	beCount, err := r.varint()
	if err != nil {
		return err
	}
	srcBEs, _ := root.Get("block_entities")
	for i := range beCount {
		if err := r.skip(3); err != nil { // packed XZ + Y
			return err
		}
		typeID, err := r.varint()
		if err != nil {
			return err
		}
		got, err := r.wireTag()
		if err != nil {
			return fmt.Errorf("block entity %d: %w (tag starts at byte %d of %d)", i, err, r.i, len(r.b))
		}
		if int(i) >= len(srcBEs.List) {
			continue // an entity the server holds itself: parsed, not compared
		}
		src := nbt.Without(srcBEs.List[i].Comp, "x", "y", "z")
		if got.Type != nbt.Compound {
			return fmt.Errorf("block entity %d: tag type %d, want compound", i, got.Type)
		}
		if err := compareTags(src, got.Comp); err != nil {
			return fmt.Errorf("block entity %d (type %d): %w", i, typeID, err)
		}
	}

	// Light: four masks, then one array per set bit of the sky and block masks.
	skyMask, err := r.bitset()
	if err != nil {
		return err
	}
	blockMask, err := r.bitset()
	if err != nil {
		return err
	}
	if _, err := r.bitset(); err != nil { // empty sky mask
		return err
	}
	if _, err := r.bitset(); err != nil { // empty block mask
		return err
	}
	for name, mask := range map[string]uint64{"sky": skyMask, "block": blockMask} {
		n, err := r.varint()
		if err != nil {
			return err
		}
		if n != int32(bits.OnesCount64(mask)) {
			return fmt.Errorf("%s light: %d arrays for %d mask bits", name, n, bits.OnesCount64(mask))
		}
		for i := range n {
			size, err := r.varint()
			if err != nil {
				return err
			}
			if size != 2048 {
				return fmt.Errorf("%s light array %d: %d bytes, want 2048", name, i, size)
			}
			if err := r.skip(int(size)); err != nil {
				return err
			}
		}
	}

	if r.remaining() != 0 {
		return fmt.Errorf("%d trailing bytes after the chunk payload", r.remaining())
	}
	return nil
}

func firstDiff(a, b []int32) int {
	for i := range a {
		if a[i] != b[i] {
			return i
		}
	}
	return 0
}

// byteReader mirrors the client's reads so the test fails the way the client
// would, not the way a permissive parser would.
type byteReader struct {
	b   []byte
	i   int
	sec *byteReader // section buffer, filled in by afterSections
}

func (r *byteReader) remaining() int { return len(r.b) - r.i }

func (r *byteReader) skip(n int) error {
	if r.remaining() < n {
		return fmt.Errorf("short read: want %d, have %d", n, r.remaining())
	}
	r.i += n
	return nil
}

func (r *byteReader) u8() (byte, error) {
	if r.remaining() < 1 {
		return 0, fmt.Errorf("short read: want 1, have 0")
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

func (r *byteReader) u16() (uint16, error) {
	if r.remaining() < 2 {
		return 0, fmt.Errorf("short read: want 2, have %d", r.remaining())
	}
	v := binary.BigEndian.Uint16(r.b[r.i:])
	r.i += 2
	return v, nil
}

func (r *byteReader) varint() (int32, error) {
	var v uint32
	for shift := uint(0); shift < 35; shift += 7 {
		b, err := r.u8()
		if err != nil {
			return 0, err
		}
		v |= uint32(b&0x7F) << shift
		if b&0x80 == 0 {
			return int32(v), nil
		}
	}
	return 0, fmt.Errorf("varint too long")
}

// bitset reads a BitSet the way vanilla does: a varint byte length followed by
// the minimal little-endian representation.
func (r *byteReader) bitset() (uint64, error) {
	n, err := r.varint()
	if err != nil {
		return 0, err
	}
	if n < 0 || n > 8 {
		return 0, fmt.Errorf("bitset byte length = %d", n)
	}
	if r.remaining() < int(n) {
		return 0, fmt.Errorf("short read in bitset")
	}
	var mask uint64
	for i := range int(n) {
		mask |= uint64(r.b[r.i+i]) << (8 * uint(i))
	}
	r.i += int(n)
	return mask, nil
}

// paletted reads a paletted container: a width byte, either a bare id (width 0)
// or a palette plus packed longs, and returns the expanded entries.
func (r *byteReader) paletted(entries, indirectMax int) ([]int32, error) {
	width, err := r.u8()
	if err != nil {
		return nil, err
	}
	if width == 0 {
		id, err := r.varint()
		if err != nil {
			return nil, err
		}
		out := make([]int32, entries)
		for i := range out {
			out[i] = id
		}
		return out, nil
	}
	palette := []int32(nil)
	if int(width) <= indirectMax {
		n, err := r.varint()
		if err != nil {
			return nil, err
		}
		if n <= 0 || n > 1<<20 {
			return nil, fmt.Errorf("palette size %d", n)
		}
		palette = make([]int32, n)
		for i := range palette {
			v, err := r.varint()
			if err != nil {
				return nil, err
			}
			palette[i] = v
		}
	}
	perLong := 64 / int(width)
	longs := (entries + perLong - 1) / perLong
	raw := make([]uint64, longs)
	for i := range raw {
		if r.remaining() < 8 {
			return nil, fmt.Errorf("short read in storage: want %d longs", longs)
		}
		raw[i] = binary.BigEndian.Uint64(r.b[r.i:])
		r.i += 8
	}
	mask := uint64(1)<<uint(width) - 1
	out := make([]int32, entries)
	for i := range out {
		idx := unpackIndex(raw, int(width), i) & mask
		if palette == nil {
			out[i] = int32(idx) // global palette: the index is the id
			continue
		}
		if idx >= uint64(len(palette)) {
			return nil, fmt.Errorf("index %d outside palette of %d", idx, len(palette))
		}
		out[i] = palette[idx]
	}
	return out, nil
}

func unpackIndex(raw []uint64, width, i int) uint64 {
	perLong := 64 / width
	li := i / perLong
	if li >= len(raw) {
		return 0
	}
	return raw[li] >> (uint(i%perLong) * uint(width))
}

// wireTag reads one wire NBT tag: a bare type byte then the payload, with no
// name (FriendlyByteBuf.writeNbt / NbtIo.writeAnyTag). It reuses the package
// reader — which is what parses real region files — by splicing in an empty
// name, then counts the payload it consumed. A zero type byte means no tag.
func (r *byteReader) wireTag() (nbt.Tag, error) {
	typ, err := r.u8()
	if err != nil {
		return nbt.Tag{}, err
	}
	if typ == 0 {
		return nbt.Tag{}, nil
	}
	rest := r.b[r.i:]
	buf := append([]byte{typ, 0, 0}, rest...)
	sub := bytes.NewReader(buf)
	if _, _, err := nbt.Read(sub); err != nil {
		return nbt.Tag{}, err
	}
	// sub consumed the type byte, the empty name and the payload, so what is
	// missing from rest is exactly the payload.
	payload := len(rest) - sub.Len()
	r.i += payload
	_, tag, err := nbt.Read(bytes.NewReader(buf[:3+payload]))
	return tag, err
}

// compareTags checks that a decoded tag matches the source it came from.
func compareTags(want, got map[string]nbt.Tag) error {
	if len(want) != len(got) {
		return fmt.Errorf("field count %d, want %d", len(got), len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			return fmt.Errorf("missing field %q", k)
		}
		if !reflect.DeepEqual(w, g) {
			return fmt.Errorf("field %q differs: %s", k, describe(w, g))
		}
	}
	return nil
}

// describe renders a mismatch compactly: tag values can be large nested
// compounds, and a raw dump of both is unreadable.
func describe(want, got nbt.Tag) string {
	w, g := summary(want), summary(got)
	if w != g {
		return fmt.Sprintf("%s != %s", w, g)
	}
	// Same shape but different content: report the first differing field.
	for k, wv := range want.Comp {
		if gv, ok := got.Comp[k]; ok && !reflect.DeepEqual(wv, gv) {
			return fmt.Sprintf("%s: %s != %s", k, summary(wv), summary(gv))
		}
	}
	for i := range want.List {
		if i < len(got.List) && !reflect.DeepEqual(want.List[i], got.List[i]) {
			return fmt.Sprintf("list[%d]: %s != %s", i, summary(want.List[i]), summary(got.List[i]))
		}
	}
	return "differs"
}

func summary(t nbt.Tag) string {
	switch t.Type {
	case nbt.String:
		return fmt.Sprintf("string(%q)", t.Str)
	case nbt.Compound:
		return fmt.Sprintf("compound(%d fields)", len(t.Comp))
	case nbt.List:
		return fmt.Sprintf("list(%d)", len(t.List))
	case nbt.ByteArray:
		return fmt.Sprintf("bytes(%d)", len(t.Bytes))
	case nbt.IntArray:
		return fmt.Sprintf("ints(%d)", len(t.Ints))
	case nbt.LongArray:
		return fmt.Sprintf("longs(%d)", len(t.Longs))
	case nbt.Double, nbt.Float:
		return fmt.Sprintf("float(%v)", t.Flt)
	default:
		return fmt.Sprintf("type%d(%d)", t.Type, t.Num)
	}
}
