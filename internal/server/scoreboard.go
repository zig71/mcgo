package server

import (
	"strconv"
	"strings"

	"mcgo/internal/mcproto"
)

// The scoreboard.
//
// An objective has a name, a display name and a criterion; a score is a number
// per holder (a player's name) per objective. The criteria that track something
// on their own are the ones this server can see: health, deaths and dummy. A
// dummy objective only changes when a command sets it, which is what most
// people use a sidebar for anyway.
type scoreObjective struct {
	name     string
	display  string
	criteria string
	// hearts renders the number as hearts rather than as a number.
	hearts bool
}

type scoreboard struct {
	objectives map[string]*scoreObjective
	// scores is objective -> holder -> value.
	scores map[string]map[string]int32
	// sidebar is the objective shown in the sidebar, if any.
	sidebar string
}

func newScoreboard() *scoreboard {
	return &scoreboard{
		objectives: map[string]*scoreObjective{},
		scores:     map[string]map[string]int32{},
	}
}

// sidebarSlot is DisplaySlot.SIDEBAR.
const sidebarSlot = 1

// Objective packet methods.
const (
	objectiveAdd    = 0
	objectiveRemove = 1
	objectiveChange = 2
)

// knownCriteria is what a player may ask an objective to count. A criterion the
// server cannot see would show a number that never moves.
var knownCriteria = map[string]bool{"dummy": true, "health": true, "deaths": true}

// addObjective creates or updates an objective and tells everyone.
func (s *Server) addObjective(name, criteria, display string) bool {
	if !knownCriteria[criteria] {
		return false
	}
	if display == "" {
		display = name
	}
	s.scoreMu.Lock()
	old, existed := s.board.objectives[name]
	obj := &scoreObjective{name: name, criteria: criteria, display: display}
	if existed {
		obj.hearts = old.hearts
	}
	s.board.objectives[name] = obj
	s.scoreMu.Unlock()

	method := int32(objectiveAdd)
	if existed {
		method = objectiveChange
	}
	s.broadcastObjective(obj, method)
	// An objective that follows something already true starts with the values
	// it should have, not from zero.
	if criteria != "dummy" {
		s.refreshObjective(obj)
	}
	return true
}

// removeObjective drops an objective, its scores and its display.
func (s *Server) removeObjective(name string) bool {
	s.scoreMu.Lock()
	obj, ok := s.board.objectives[name]
	if ok {
		delete(s.board.objectives, name)
		delete(s.board.scores, name)
		if s.board.sidebar == name {
			s.board.sidebar = ""
		}
	}
	s.scoreMu.Unlock()
	if !ok {
		return false
	}
	s.broadcastObjective(obj, objectiveRemove)
	if s.board.sidebar == "" {
		s.broadcastDisplay("")
	}
	return true
}

// setSidebar shows an objective in the sidebar, or clears it with an empty name.
func (s *Server) setSidebar(name string) bool {
	if name != "" {
		s.scoreMu.Lock()
		_, ok := s.board.objectives[name]
		s.scoreMu.Unlock()
		if !ok {
			return false
		}
	}
	s.scoreMu.Lock()
	s.board.sidebar = name
	s.scoreMu.Unlock()
	s.broadcastDisplay(name)
	return true
}

// setScore sets a holder's score in an objective.
func (s *Server) setScore(holder, objective string, value int32) bool {
	s.scoreMu.Lock()
	if _, ok := s.board.objectives[objective]; !ok {
		s.scoreMu.Unlock()
		return false
	}
	if s.board.scores[objective] == nil {
		s.board.scores[objective] = map[string]int32{}
	}
	s.board.scores[objective][holder] = value
	s.scoreMu.Unlock()

	s.broadcastScore(objective, holder, value)
	return true
}

// trackScore feeds a criterion: everything counting it learns the new number.
func (s *Server) trackScore(criteria, holder string, value int32) {
	s.scoreMu.Lock()
	var names []string
	for name, obj := range s.board.objectives {
		if obj.criteria == criteria {
			if s.board.scores[name] == nil {
				s.board.scores[name] = map[string]int32{}
			}
			s.board.scores[name][holder] = value
			names = append(names, name)
		}
	}
	s.scoreMu.Unlock()
	for _, name := range names {
		s.broadcastScore(name, holder, value)
	}
}

// refreshObjective fills an objective from what the server knows right now.
func (s *Server) refreshObjective(obj *scoreObjective) {
	switch obj.criteria {
	case "health":
		for _, sess := range s.sessionList() {
			s.trackScore("health", sess.name, int32(sess.health+0.5))
		}
	case "deaths":
		// Deaths are counted from here on: this server does not remember a
		// player's score between sessions.
	}
}

// bumpDeaths counts a player's deaths and returns the new total. This server
// does not remember them between sessions, so the count starts at one.
func (s *Server) bumpDeaths(name string) int32 {
	s.scoreMu.Lock()
	defer s.scoreMu.Unlock()
	if s.deaths == nil {
		s.deaths = map[string]int32{}
	}
	s.deaths[name]++
	return s.deaths[name]
}

// broadcastObjective sends an objective to everyone.
func (s *Server) broadcastObjective(obj *scoreObjective, method int32) {
	for _, sess := range s.sessionList() {
		var w mcproto.Writer
		w.WriteString(obj.name)
		w.WriteByte(byte(method))
		if method != objectiveRemove {
			w.WriteBytes(nbtText(obj.display))
			render := int32(0) // INTEGER
			if obj.hearts {
				render = 1 // HEARTS
			}
			w.WriteVarInt(render)
			w.WriteBool(false) // no number format
		}
		_ = sess.sendQuiet(sess.t.PlayClientboundSetObjective, w.Bytes())
	}
}

// broadcastScore sends one score to everyone.
func (s *Server) broadcastScore(objective, holder string, value int32) {
	for _, sess := range s.sessionList() {
		if sess.t.PlayClientboundSetScore == 0 {
			continue
		}
		var w mcproto.Writer
		w.WriteString(holder)
		w.WriteString(objective)
		w.WriteVarInt(value)
		w.WriteBool(false) // no display name
		w.WriteBool(false) // no number format
		_ = sess.sendQuiet(sess.t.PlayClientboundSetScore, w.Bytes())
	}
}

// broadcastDisplay says which objective the sidebar shows, empty for none.
func (s *Server) broadcastDisplay(name string) {
	for _, sess := range s.sessionList() {
		if sess.t.PlayClientboundSetDisplayObjective == 0 {
			continue
		}
		var w mcproto.Writer
		w.WriteVarInt(sidebarSlot)
		w.WriteString(name)
		_ = sess.sendQuiet(sess.t.PlayClientboundSetDisplayObjective, w.Bytes())
	}
}

// sendScoreboard is everything a joining player needs: the objectives, their
// scores, and which one is in the sidebar.
func (s *Server) sendScoreboard(sess *session) {
	s.scoreMu.Lock()
	objectives := make([]*scoreObjective, 0, len(s.board.objectives))
	for _, obj := range s.board.objectives {
		objectives = append(objectives, obj)
	}
	scores := map[string]map[string]int32{}
	for name, holders := range s.board.scores {
		copied := make(map[string]int32, len(holders))
		for holder, value := range holders {
			copied[holder] = value
		}
		scores[name] = copied
	}
	s.sidebar = s.board.sidebar
	s.scoreMu.Unlock()

	saved := s.sidebar
	for _, obj := range objectives {
		var w mcproto.Writer
		w.WriteString(obj.name)
		w.WriteByte(byte(objectiveAdd))
		w.WriteBytes(nbtText(obj.display))
		if obj.hearts {
			w.WriteVarInt(1)
		} else {
			w.WriteVarInt(0)
		}
		w.WriteBool(false)
		_ = sess.sendQuiet(sess.t.PlayClientboundSetObjective, w.Bytes())
		for holder, value := range scores[obj.name] {
			var sw mcproto.Writer
			sw.WriteString(holder)
			sw.WriteString(obj.name)
			sw.WriteVarInt(value)
			sw.WriteBool(false)
			sw.WriteBool(false)
			_ = sess.sendQuiet(sess.t.PlayClientboundSetScore, sw.Bytes())
		}
	}
	if saved != "" {
		var w mcproto.Writer
		w.WriteVarInt(sidebarSlot)
		w.WriteString(saved)
		_ = sess.sendQuiet(sess.t.PlayClientboundSetDisplayObjective, w.Bytes())
	}
}

// runScoreboard is the /scoreboard command, in the shape vanilla uses.
func (s *session) runScoreboard(fields []string) string {
	if len(fields) < 2 {
		return "Usage: /scoreboard <objectives|players> ..."
	}
	switch fields[1] {
	case "objectives":
		if len(fields) < 3 {
			return "Usage: /scoreboard objectives <add|remove|setdisplay> ..."
		}
		switch fields[2] {
		case "add":
			if len(fields) < 5 {
				return "Usage: /scoreboard objectives add <name> <criteria> [display name]"
			}
			display := ""
			if len(fields) > 5 {
				display = strings.Join(fields[5:], " ")
			}
			if !s.srv.addObjective(fields[3], fields[4], display) {
				return "Unknown criterion: " + fields[4] + " (dummy, health, deaths)"
			}
			return "Added objective " + fields[3]
		case "remove":
			if len(fields) < 4 {
				return "Usage: /scoreboard objectives remove <name>"
			}
			if !s.srv.removeObjective(fields[3]) {
				return "Unknown objective: " + fields[3]
			}
			return "Removed objective " + fields[3]
		case "setdisplay":
			if len(fields) < 5 {
				return "Usage: /scoreboard objectives setdisplay sidebar <name>"
			}
			if fields[3] != "sidebar" {
				return "Only the sidebar is displayed for now"
			}
			if !s.srv.setSidebar(fields[4]) {
				return "Unknown objective: " + fields[4]
			}
			return "Objective " + fields[4] + " is now shown in the sidebar"
		}
	case "players":
		if len(fields) < 6 || fields[2] != "set" {
			return "Usage: /scoreboard players set <holder> <objective> <score>"
		}
		value, err := strconv.ParseInt(fields[5], 10, 32)
		if err != nil {
			return "Not a number: " + fields[5]
		}
		if !s.srv.setScore(fields[3], fields[4], int32(value)) {
			return "Unknown objective: " + fields[4]
		}
		return "Set " + fields[3] + "'s " + fields[4] + " to " + strconv.Itoa(int(value))
	}
	return "Unknown scoreboard command: " + fields[1]
}
