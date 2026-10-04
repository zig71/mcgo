package server

import (
	"encoding/binary"
	"log"
	"sort"

	"mcgo/internal/mcproto"
)

// enterConfiguration runs the Configuration state up to the hand-off to Play.
func (s *session) enterConfiguration() {
	var w mcproto.Writer

	// 0. Server brand. Vanilla sends this first, as a custom payload on channel
	// minecraft:brand carrying the brand string. Without it a client reports the
	// brand as null — visible in crash reports as `Server brand: ~~NULL~~`.
	//
	// Published id: configuration, clientbound, 0x01 (verified against a real
	// 26.3 server's packet stream).
	var brand mcproto.Writer
	brand.WriteString("minecraft:brand")
	brand.WriteString("mcgo")
	if err := s.send(1, brand.Bytes()); err != nil {
		return
	}

	// 1. Enabled features.
	w.WriteVarInt(1)
	w.WriteString("minecraft:vanilla")
	if err := s.send(s.t.ConfigClientboundUpdateEnabledFeatures, w.Bytes()); err != nil {
		return
	}

	// 2. Select known packs. The client selects nothing, so the registry data
	// below always carries full NBT; the negotiation is kept because a client
	// that does select minecraft:core would let us skip it.
	w.Reset()
	w.WriteVarInt(1)
	w.WriteString("minecraft")
	w.WriteString("core")
	w.WriteString(s.t.KnownPack)
	if err := s.send(s.t.ConfigClientboundSelectKnownPacks, w.Bytes()); err != nil {
		return
	}

	// 3. Await the client's known-pack selection.
	coreKnown, ok := s.awaitKnownPacks()
	if !ok {
		return
	}

	// 4. Registry data. Entry order defines the raw IDs the client assigns.
	//
	// Skip contents when the client declared minecraft:core, exactly as vanilla
	// does. Measured against a real 26.3 server: every registry's entries arrive
	// as names only, at 1637/223/326 bytes rather than tens of KB. Sending the
	// contents anyway is legal but it is not what the client is built to
	// receive, and it is not what this server did while it worked.
	nbt := s.t.RegistryNBT
	off := 0

	for _, reg := range s.t.RegistryOrder {
		entries := s.t.RegistryEntries[reg]
		w.Reset()
		w.WriteString(reg)
		w.WriteVarInt(int32(len(entries)))
		for _, e := range entries {
			w.WriteString(e)
			if coreKnown {
				w.WriteByte(0) // Prefixed Optional NBT: sourced from the pack
				continue
			}
			if off+4 > len(nbt) {
				// The blob does not cover this entry. Send it as absent so the
				// client falls back to its own pack; reading past the end would
				// hand it garbage, and aborting the whole configuration over one
				// entry is worse than the client sourcing that entry itself.
				w.WriteByte(0)
				continue
			}
			n := int(binary.BigEndian.Uint32(nbt[off:]))
			off += 4
			if off+n > len(nbt) {
				log.Printf("config: registry NBT blob truncated at %s", e)
				return
			}
			w.WriteByte(1) // Prefixed Optional NBT: present
			w.WriteBytes(nbt[off : off+n])
			off += n
		}
		if err := s.send(s.t.ConfigClientboundRegistryData, w.Bytes()); err != nil {
			return
		}
	}

	// 5. Update tags (raw IDs, expanded from the data pack).
	regs := sortedKeys(s.t.RegistryTags)
	w.Reset()
	w.WriteVarInt(int32(len(regs)))
	for _, reg := range regs {
		tags := s.t.RegistryTags[reg]
		names := sortedKeys(tags)
		w.WriteString(reg)
		w.WriteVarInt(int32(len(names)))
		for _, t := range names {
			ids := tags[t]
			w.WriteString(t)
			w.WriteVarInt(int32(len(ids)))
			for _, id := range ids {
				w.WriteVarInt(id)
			}
		}
	}
	if err := s.send(s.t.ConfigClientboundUpdateTags, w.Bytes()); err != nil {
		return
	}

	// 6. Finish configuration.
	if err := s.send(s.t.ConfigClientboundFinishConfiguration, nil); err != nil {
		return
	}
	if !s.awaitServerbound(s.t.ConfigServerboundFinishConfiguration, "finish configuration") {
		return
	}

	log.Printf("configuration complete for %q; entering play state", s.name)
	s.enterPlay()
}

// awaitKnownPacks waits for the client's pack selection and logs it, since an
// unrecognised core pack means the client cannot source registry data locally.
func (s *session) awaitKnownPacks() (coreKnown, ok bool) {
	for {
		p, err := s.c.ReadPacket()
		if err != nil {
			log.Printf("config: read failed while awaiting known packs: %v", err)
			return false, false
		}
		switch p.ID {
		case s.t.ConfigServerboundSelectKnownPacks:
			r := mcproto.NewReader(p.Data)
			n, err := r.ReadVarInt()
			if err != nil {
				log.Printf("config: malformed known packs: %v", err)
				return false, false
			}
			log.Printf("<- client knows %d packs:", n)
			for range n {
				ns, e1 := r.ReadString()
				id, e2 := r.ReadString()
				ver, e3 := r.ReadString()
				if e1 != nil || e2 != nil || e3 != nil {
					log.Printf("config: malformed pack entry: %v %v %v", e1, e2, e3)
					return false, false
				}
				log.Printf("   %s:%s %s", ns, id, ver)
				if ns == "minecraft" && id == "core" {
					coreKnown = true
				}
			}
			return coreKnown, true
		case s.t.ConfigServerboundClientInformation:
			s.handleClientInformation(p)
		case s.t.ConfigServerboundCustomPayload,
			s.t.ConfigServerboundCookieResponse,
			s.t.ConfigServerboundKeepAlive,
			s.t.ConfigServerboundPong:
			log.Printf("<- config 0x%02X len=%d (ignored)", p.ID, len(p.Data))
		default:
			log.Printf("config: unexpected serverbound 0x%02X while awaiting known packs", p.ID)
			return false, false
		}
	}
}

// awaitServerbound reads configuration packets, ignoring the informational
// ones, until it sees want.
func (s *session) awaitServerbound(want int32, what string) bool {
	for {
		p, err := s.c.ReadPacket()
		if err != nil {
			log.Printf("config: read failed while awaiting %s: %v", what, err)
			return false
		}
		switch p.ID {
		case want:
			return true
		case s.t.ConfigServerboundClientInformation,
			s.t.ConfigServerboundCustomPayload,
			s.t.ConfigServerboundCookieResponse,
			s.t.ConfigServerboundKeepAlive,
			s.t.ConfigServerboundPong:
			log.Printf("<- config 0x%02X len=%d (ignored)", p.ID, len(p.Data))
		default:
			log.Printf("config: unexpected serverbound 0x%02X while awaiting %s", p.ID, what)
			return false
		}
	}
}

// handleClientInformation reads what the client says about itself. Only the
// skin-layer mask is kept: it is the one field the other players need, since it
// rides in entity metadata.
func (s *session) handleClientInformation(p *mcproto.Packet) {
	r := mcproto.NewReader(p.Data)
	if _, err := r.ReadString(); err != nil { // language
		return
	}
	if _, err := r.ReadByte(); err != nil { // view distance
		return
	}
	if _, err := r.ReadVarInt(); err != nil { // chat visibility
		return
	}
	if _, err := r.ReadByte(); err != nil { // chat colours
		return
	}
	parts, err := r.ReadByte()
	if err != nil { // the skin-layer mask
		return
	}
	s.skinParts, s.haveSkinParts = parts, true
	log.Printf("<- client information: skin layers 0x%02X", parts)
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// registryID returns the raw id of name within an ordered registry entry list.
func registryID(entries []string, name string) int32 {
	for i, e := range entries {
		if e == name {
			return int32(i)
		}
	}
	return 0
}
