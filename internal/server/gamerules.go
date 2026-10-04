package server

import (
	"strconv"
	"strings"
)

// Game rules.
//
// The client has its own list of rules, sent as data-pack NBT in the registry
// blob, and its settings screen reads from that copy: changing a value there
// would mean rewriting the blob. So these are the rules the *server* acts on,
// which is what makes them real rather than cosmetic, and the client's screen
// goes on showing what it was sent.
type gameRules struct {
	daylightCycle    bool
	tileDrops        bool
	mobLoot          bool
	keepInventory    bool
	immediateRespawn bool
}

func defaultGameRules() gameRules {
	return gameRules{
		daylightCycle:    true,
		tileDrops:        true,
		mobLoot:          true,
		keepInventory:    true,
		immediateRespawn: false,
	}
}

// ruleNames is the rules this server honours, in the order they are listed to a
// player. A rule that is not here is one the server would only pretend to keep.
var ruleNames = []string{
	"doDaylightCycle",
	"doTileDrops",
	"doMobLoot",
	"keepInventory",
	"doImmediateRespawn",
}

// rule reads a rule by its vanilla name.
func (s *Server) rule(name string) (bool, bool) {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	r := s.rules
	switch name {
	case "doDaylightCycle":
		return r.daylightCycle, true
	case "doTileDrops":
		return r.tileDrops, true
	case "doMobLoot":
		return r.mobLoot, true
	case "keepInventory":
		return r.keepInventory, true
	case "doImmediateRespawn":
		return r.immediateRespawn, true
	}
	return false, false
}

// setRule changes a rule value, reporting whether the name was one of ours.
func (s *Server) setRule(name string, value bool) bool {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	switch name {
	case "doDaylightCycle":
		s.rules.daylightCycle = value
	case "doTileDrops":
		s.rules.tileDrops = value
	case "doMobLoot":
		s.rules.mobLoot = value
	case "keepInventory":
		s.rules.keepInventory = value
	case "doImmediateRespawn":
		s.rules.immediateRespawn = value
	default:
		return false
	}
	return true
}

// ruleList is what a bare /gamerule shows.
func (s *Server) ruleList() string {
	var b strings.Builder
	for i, name := range ruleNames {
		if i > 0 {
			b.WriteString(", ")
		}
		value, _ := s.rule(name)
		b.WriteString(name)
		b.WriteString("=")
		b.WriteString(strconv.FormatBool(value))
	}
	return b.String()
}
