package world

import (
	"bytes"
	"fmt"
	"sort"

	"mcgo/internal/mcproto"
	"mcgo/internal/nbt"
)

// ChunkContext is what converting a saved chunk needs from the version tables.
type ChunkContext struct {
	Table            *BlockStateTable
	Biomes           map[string]int32 // biome name -> raw id (registry order)
	BlockEntityTypes map[string]int32 // block entity id -> registry id
	MinY             int
	Sections         int
	// Edits overrides block state ids by index within the chunk, laid out
	// (y<<8)|(z<<4)|x, which is the order the packed data uses. A saved chunk
	// with edits re-converts rather than being patched in place.
	Edits map[int]int32
	// ExtraBEs are block entities the server holds itself — signs a player
	// placed, for instance — which the region file cannot know about. They are
	// written alongside the file's own.
	ExtraBEs []ExtraBE
}

// ExtraBE is one block entity to send with a chunk.
type ExtraBE struct {
	X, Y, Z int32  // world coordinates
	Type    int32  // block_entity_type registry id
	NBT     []byte // tag type byte and payload, as the wire wants it
}

// heightmapIDs maps the saved heightmap names to the wire enum. Only the three
// types whose Usage is CLIENT are sent: Heightmap.Types.sendToClient filters
// WORLD_SURFACE_WG, OCEAN_FLOOR_WG and OCEAN_FLOOR out, and the wire ids are the
// enum's own ids, not a dense 0..n sequence of the sent ones.
var heightmapIDs = map[string]int32{
	"WORLD_SURFACE":             1,
	"MOTION_BLOCKING":           4,
	"MOTION_BLOCKING_NO_LEAVES": 5,
}

// ConvertChunk turns saved chunk NBT into a level_chunk_with_light body.
//
// This is a real translation, not a copy: the saved palette names blocks while
// the wire wants numeric ids, the saved light lives inside each section while
// the wire carries masks plus flat arrays, and the padded sections the client
// derives from the dimension height may not exist in the file at all.
func ConvertChunk(raw []byte, cx, cz int32, ctx ChunkContext) ([]byte, error) {
	_, root, err := nbt.Read(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("chunk (%d,%d): %w", cx, cz, err)
	}
	sectionsTag, ok := root.Get("sections")
	if !ok || len(sectionsTag.List) == 0 {
		return nil, fmt.Errorf("chunk (%d,%d): no sections", cx, cz)
	}
	byY := make(map[int]nbt.Tag, len(sectionsTag.List))
	for _, s := range sectionsTag.List {
		y, _ := s.Get("Y")
		byY[int(int8(y.Num))] = s
	}

	var w mcproto.Writer
	w.WriteInt(cx)
	w.WriteInt(cz)
	writeHeightmaps(&w, root)

	var cd mcproto.Writer
	for i := range ctx.Sections {
		y := ctx.MinY/16 + i
		if s, ok := byY[y]; ok {
			if err := encodeSavedSection(&cd, s, ctx); err != nil {
				return nil, fmt.Errorf("chunk (%d,%d) section %d: %w", cx, cz, y, err)
			}
		} else {
			cd.WriteUShort(0) // block count
			cd.WriteUShort(0) // fluid count
			cd.WriteByte(0)   // blocks: single valued air
			cd.WriteVarInt(0)
			cd.WriteByte(0) // biomes: single valued
			cd.WriteVarInt(0)
		}
	}
	w.WriteVarInt(int32(cd.Len()))
	w.WriteBytes(cd.Bytes())

	if err := writeBlockEntities(&w, root, ctx, cx, cz); err != nil {
		return nil, err
	}
	writeSavedLight(&w, byY, ctx)
	return w.Bytes(), nil
}

// writeBlockEntities re-emits saved block entities in wire form: packed XZ, Y,
// the type's registry id, then the entity's NBT with its position removed —
// the packet already carries the position, and the wiki is explicit that the
// chunk's NBT excludes x, y and z.
func writeBlockEntities(w *mcproto.Writer, root nbt.Tag, ctx ChunkContext, cx, cz int32) error {
	bes, _ := root.Get("block_entities")
	extra := 0
	for _, e := range ctx.ExtraBEs {
		if e.X>>4 == cx && e.Z>>4 == cz {
			extra++
		}
	}
	if len(bes.List) == 0 && extra == 0 {
		w.WriteVarInt(0)
		return nil
	}
	w.WriteVarInt(int32(len(bes.List) + extra))
	for _, be := range bes.List {
		x, _ := be.Get("x")
		y, _ := be.Get("y")
		z, _ := be.Get("z")
		id, _ := be.Get("id")
		typeID, ok := ctx.BlockEntityTypes[id.Str]
		if !ok {
			return fmt.Errorf("unknown block entity type %q", id.Str)
		}
		w.WriteByte(byte((x.Num&15)<<4 | (z.Num & 15)))
		w.WriteUShort(uint16(int16(y.Num)))
		w.WriteVarInt(typeID)

		var buf bytes.Buffer
		tag := nbt.Tag{Type: nbt.Compound, Comp: nbt.Without(be.Comp, "x", "y", "z")}
		if err := nbt.WriteUnnamed(&buf, tag); err != nil {
			return err
		}
		// The field is OPTIONAL_COMPOUND_TAG, which is FriendlyByteBuf.writeNbt:
		// a bare tag-type byte then the payload, with no name. An absent tag is
		// written as a single zero byte (EndTag).
		w.WriteBytes(buf.Bytes())
	}
	for _, e := range ctx.ExtraBEs {
		if e.X>>4 != cx || e.Z>>4 != cz {
			continue
		}
		w.WriteByte(byte((e.X&15)<<4 | (e.Z & 15)))
		w.WriteUShort(uint16(int16(e.Y)))
		w.WriteVarInt(e.Type)
		w.WriteBytes(e.NBT)
	}
	return nil
}

// encodeSavedSection writes one section in wire order: block count, fluid
// count, the block container, then the biome container.
func encodeSavedSection(w *mcproto.Writer, s nbt.Tag, ctx ChunkContext) error {
	blocks, ok := s.Get("block_states")
	if !ok {
		// A section can carry light without a container, which means air.
		w.WriteUShort(0)
		w.WriteUShort(0)
		w.WriteByte(0)
		w.WriteVarInt(0)
	} else {
		values, err := blockValues(blocks, ctx)
		if err != nil {
			return err
		}
		nonEmpty := 0
		for _, v := range values {
			if v != 0 {
				nonEmpty++
			}
		}
		w.WriteUShort(uint16(nonEmpty))
		w.WriteUShort(0) // fluid count: not tracked in the saved format
		writePaletted(w, values, 4)
	}

	if biomes, ok := s.Get("biomes"); ok {
		biomeValues, err := biomeValues(biomes, ctx)
		if err != nil {
			return err
		}
		writePalettedBiomes(w, biomeValues, len(ctx.Biomes))
		return nil
	}
	w.WriteByte(0)
	w.WriteVarInt(0)
	return nil
}

// paletteState decodes one saved palette entry into a block name and the
// properties the file lists for it.
//
// A state is written in one of three shapes: a bare string (with properties
// inline as "name[a=b]" if any), a compound of id and properties for a
// non-default state, or a compound whose single empty key holds the block name
// for a default state. Older worlds instead use a compound of Name and
// Properties.
func paletteState(entry nbt.Tag) (string, map[string]string, error) {
	switch entry.Type {
	case nbt.String:
		name, props := ParseStateString(entry.Str)
		return name, props, nil
	case nbt.Compound:
		if id, ok := entry.Get("id"); ok {
			return id.Str, propertyMap(entry), nil
		}
		if n, ok := entry.Get("Name"); ok {
			return n.Str, propertyMap(entry), nil
		}
		if v, ok := entry.Comp[""]; ok && v.Type == nbt.String {
			return v.Str, nil, nil
		}
		keys := make([]string, 0, len(entry.Comp))
		for k := range entry.Comp {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return "", nil, fmt.Errorf("palette entry has keys %v", keys)
	default:
		return "", nil, fmt.Errorf("palette entry has tag type %d", entry.Type)
	}
}

// propertyMap reads a palette entry's property map. The keys are the same in
// both compound shapes: "properties" for this version, "Properties" older.
func propertyMap(entry nbt.Tag) map[string]string {
	for _, key := range []string{"properties", "Properties"} {
		p, ok := entry.Get(key)
		if !ok {
			continue
		}
		out := make(map[string]string, len(p.Comp))
		for k, v := range p.Comp {
			out[k] = v.Str
		}
		return out
	}
	return nil
}

// blockValues expands a saved block palette into 4096 global block state ids.
// The saved minimum width for blocks is 4, matching the wire's.
func blockValues(blocks nbt.Tag, ctx ChunkContext) ([]int32, error) {
	palette, ok := blocks.Get("palette")
	if !ok || len(palette.List) == 0 {
		return nil, fmt.Errorf("missing palette")
	}
	ids := make([]int32, 0, len(palette.List))
	for _, entry := range palette.List {
		name, props, err := paletteState(entry)
		if err != nil {
			return nil, err
		}
		id, ok := ctx.Table.Lookup(name, props)
		if !ok {
			return nil, fmt.Errorf("unknown block %s %v", name, props)
		}
		ids = append(ids, id)
	}
	if len(ids) == 1 {
		out := make([]int32, 4096)
		for i := range out {
			out[i] = ids[0]
		}
		// Edits apply here too. This branch used to return straight away, which
		// dropped every edit in a uniform section — and a section is uniform
		// whenever the saved chunk has one block in it, which most of the air
		// and most of the stone are. That is why broken blocks came back on a
		// chunk resend while placed ones did not.
		for i, id := range ctx.Edits {
			if i >= 0 && i < len(out) {
				out[i] = id
			}
		}
		return out, nil
	}
	data, ok := blocks.Get("data")
	if !ok {
		return nil, fmt.Errorf("palette of %d without data", len(ids))
	}
	bits := 4
	for (1 << uint(bits)) < len(ids) {
		bits++
	}
	idx := unpackLongs(data.Longs, bits, 4096)
	out := make([]int32, 4096)
	for i, v := range idx {
		if int(v) >= len(ids) {
			return nil, fmt.Errorf("palette index %d out of range", v)
		}
		out[i] = ids[v]
	}
	for i, id := range ctx.Edits {
		if i >= 0 && i < len(out) {
			out[i] = id
		}
	}
	return out, nil
}

// biomeValues expands a saved biome palette into 64 raw biome ids.
func biomeValues(biomes nbt.Tag, ctx ChunkContext) ([]int32, error) {
	palette, ok := biomes.Get("palette")
	if !ok || len(palette.List) == 0 {
		return make([]int32, 64), nil
	}
	ids := make([]int32, 0, len(palette.List))
	for _, entry := range palette.List {
		id, ok := ctx.Biomes[entry.Str]
		if !ok {
			return nil, fmt.Errorf("unknown biome %s", entry.Str)
		}
		ids = append(ids, id)
	}
	if len(ids) == 1 {
		out := make([]int32, 64)
		for i := range out {
			out[i] = ids[0]
		}
		return out, nil
	}
	data, ok := biomes.Get("data")
	if !ok {
		return nil, fmt.Errorf("biome palette of %d without data", len(ids))
	}
	bits := 1
	for (1 << uint(bits)) < len(ids) {
		bits++
	}
	idx := unpackLongs(data.Longs, bits, 64)
	out := make([]int32, 64)
	for i, v := range idx {
		if int(v) >= len(ids) {
			return nil, fmt.Errorf("biome index %d out of range", v)
		}
		out[i] = ids[v]
	}
	return out, nil
}

// unpackLongs reads count values of the given bit width, no entry spanning a
// long — the same packing the wire uses.
func unpackLongs(longs []int64, bits, count int) []uint32 {
	// valuesPerLong matches SimpleBitStorage: 64/bits rounding down, so a value
	// never straddles a long boundary.
	perLong := 64 / bits
	mask := uint64(1)<<uint(bits) - 1
	out := make([]uint32, count)
	for i := range count {
		li := i / perLong
		if li >= len(longs) {
			break
		}
		off := uint(i%perLong) * uint(bits)
		out[i] = uint32(uint64(longs[li]) >> off & mask)
	}
	return out
}

// writePalettedBiomes emits a biome container: 1-3 bits indirect, otherwise a
// global palette whose width is ceil(log2(registry size)).
//
// That width is not free: Strategy.globalPaletteBitsInMemory derives it from the
// biome registry, and a fixed 6 bits silently truncates any biome id above 63 —
// the client would read a different biome rather than error.
func writePalettedBiomes(w *mcproto.Writer, values []int32, registrySize int) {
	seen := make(map[int32]bool, 8)
	var palette []int32
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			palette = append(palette, v)
		}
	}
	sort.Slice(palette, func(i, j int) bool { return palette[i] < palette[j] })
	if len(palette) == 1 {
		w.WriteByte(0)
		w.WriteVarInt(palette[0])
		return
	}
	bits := 1
	for (1 << uint(bits)) < len(palette) {
		bits++
	}
	direct := bits > 3
	if direct {
		if registrySize < len(palette) {
			registrySize = len(palette)
		}
		bits = 1
		for (1 << uint(bits)) < registrySize {
			bits++
		}
	}
	index := make(map[int32]uint64, len(palette))
	for i, v := range palette {
		index[v] = uint64(i)
	}
	w.WriteByte(byte(bits))
	if !direct {
		w.WriteVarInt(int32(len(palette)))
		for _, v := range palette {
			w.WriteVarInt(v)
		}
	}
	perLong := 64 / bits
	longs := make([]int64, (len(values)+perLong-1)/perLong)
	for i, v := range values {
		idx := uint64(v)
		if !direct {
			idx = index[v]
		}
		longs[i/perLong] |= int64(idx << (uint(i%perLong) * uint(bits)))
	}
	for _, l := range longs {
		w.WriteLong(l)
	}
}

// writeHeightmaps converts the saved heightmap compound into the wire's
// prefixed array of {enum, longs}.
func writeHeightmaps(w *mcproto.Writer, root nbt.Tag) {
	hm, ok := root.Get("Heightmaps")
	if !ok {
		w.WriteVarInt(0)
		return
	}
	names := make([]string, 0, len(hm.Comp))
	for name := range hm.Comp {
		if _, known := heightmapIDs[name]; known {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	w.WriteVarInt(int32(len(names)))
	for _, name := range names {
		w.WriteVarInt(heightmapIDs[name])
		longs := hm.Comp[name].Longs
		w.WriteVarInt(int32(len(longs)))
		for _, l := range longs {
			w.WriteLong(l)
		}
	}
}

// writeSavedLight turns per-section SkyLight/BlockLight arrays into the wire's
// masks plus flat arrays. Mask bit i+1 covers section i, because bit 0 is the
// section below the world.
func writeSavedLight(w *mcproto.Writer, byY map[int]nbt.Tag, ctx ChunkContext) {
	var skyMask, blockMask, emptyBlockMask uint64
	var sky, block, emptyBlock [][]byte

	for i := range ctx.Sections {
		bit := uint(i) + 1
		s, ok := byY[ctx.MinY/16+i]
		if ok {
			if v, ok := s.Get("SkyLight"); ok && len(v.Bytes) > 0 {
				skyMask |= 1 << bit
				sky = append(sky, v.Bytes)
			}
			if v, ok := s.Get("BlockLight"); ok && len(v.Bytes) > 0 {
				blockMask |= 1 << bit
				block = append(block, v.Bytes)
			} else {
				emptyBlockMask |= 1 << bit
				emptyBlock = append(emptyBlock, make([]byte, 2048))
			}
			continue
		}
		emptyBlockMask |= 1 << bit
		emptyBlock = append(emptyBlock, make([]byte, 2048))
	}

	writeBitSet(w, skyMask)
	writeBitSet(w, blockMask)
	writeBitSet(w, 0)
	writeBitSet(w, emptyBlockMask)

	// Only two arrays: one per set bit of the sky mask, one per set bit of the
	// block mask. Bits in an *empty* mask carry no data at all, which is the
	// point of declaring them empty.
	w.WriteVarInt(int32(len(sky)))
	for _, a := range sky {
		writeLightArray(w, a)
	}
	w.WriteVarInt(int32(len(block)))
	for _, a := range block {
		writeLightArray(w, a)
	}
}

func writeLightArray(w *mcproto.Writer, a []byte) {
	w.WriteVarInt(int32(len(a)))
	w.WriteBytes(a)
}
