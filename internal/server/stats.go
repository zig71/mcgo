package server

import (
	"mcgo/internal/mcproto"
)

// Statistics.
//
// The client has a statistics screen of its own, and it fills it from one
// packet: a map of statistic to count. Only a few are counted here, and the
// point of naming them is that a count the server does not keep would show as a
// zero the player cannot explain.
//
// A statistic on the wire is a pair: which kind (mined, crafted, custom, ...)
// and which one of that kind. For a custom statistic the second id is the
// custom-statistic registry's; for mined it is the block registry's.
const (
	statCustom = "minecraft:custom"
	statMined  = "minecraft:mined"

	// Custom statistics this server counts.
	statDeaths   = "minecraft:deaths"
	statPlayTime = "minecraft:play_time"
)

// countStat increments one player's statistic.
func (s *Server) countStat(uuid [16]byte, kind, value string) {
	s.statMu.Lock()
	if s.stats == nil {
		s.stats = map[[16]byte]map[string]map[string]int32{}
	}
	if s.stats[uuid] == nil {
		s.stats[uuid] = map[string]map[string]int32{}
	}
	if s.stats[uuid][kind] == nil {
		s.stats[uuid][kind] = map[string]int32{}
	}
	s.stats[uuid][kind][value]++
	s.statMu.Unlock()
}

// statValue reads one player's statistic, for tests and for anything that wants
// to show one.
func (s *Server) statValue(uuid [16]byte, kind, value string) int32 {
	s.statMu.Lock()
	defer s.statMu.Unlock()
	return s.stats[uuid][kind][value]
}

// statSnapshot copies one player's counters, so the packet can be built without
// holding the lock while it sends.
func (s *Server) statSnapshot(uuid [16]byte) map[string]map[string]int32 {
	s.statMu.Lock()
	defer s.statMu.Unlock()
	out := map[string]map[string]int32{}
	for kind, values := range s.stats[uuid] {
		copied := make(map[string]int32, len(values))
		for value, count := range values {
			copied[value] = count
		}
		out[kind] = copied
	}
	return out
}

// statIDs resolves a statistic's two ids in one protocol version.
func (s *session) statIDs(kind, value string) (int32, int32, bool) {
	t := s.t
	kindID, ok := t.StatTypeIDs[kind]
	if !ok {
		return 0, 0, false
	}
	switch kind {
	case statCustom:
		id, ok := t.CustomStatIDs[value]
		return kindID, id, ok
	case statMined:
		id, ok := t.BlockIDs[value]
		return kindID, id, ok
	}
	return 0, 0, false
}

// sendStatistics is the packet behind the client's statistics screen: how many
// entries there are, then each as a kind, a value and a count.
func (s *session) sendStatistics() {
	if s.t.PlayClientboundAwardStatistics == 0 {
		return
	}
	snapshot := s.srv.statSnapshot(s.uuid)
	// Play time is derived rather than ticked: it is how much of the world's
	// age has passed since this player joined.
	if snapshot[statCustom] == nil {
		snapshot[statCustom] = map[string]int32{}
	}
	played := s.srv.Age() - s.joinAge
	if played < 0 {
		played = 0
	}
	snapshot[statCustom][statPlayTime] = int32(played)

	type entry struct{ kind, value, count int32 }
	var entries []entry
	for kind, values := range snapshot {
		for value, count := range values {
			kindID, valueID, ok := s.statIDs(kind, value)
			if !ok {
				continue
			}
			entries = append(entries, entry{kindID, valueID, count})
		}
	}

	var w mcproto.Writer
	w.WriteVarInt(int32(len(entries)))
	for _, e := range entries {
		w.WriteVarInt(e.kind)
		w.WriteVarInt(e.value)
		w.WriteVarInt(e.count)
	}
	_ = s.send(s.t.PlayClientboundAwardStatistics, w.Bytes())
}
