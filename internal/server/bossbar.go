package server

import (
	"crypto/sha256"
	"strconv"
	"strings"

	"mcgo/internal/mcproto"
)

// Boss bars: a named bar at the top of everyone's screen, with a progress and a
// colour. Vanilla uses them for bosses and any server plugin uses them for
// whatever it likes; here they are a command.
//
// The name can be set when the bar is created but not changed afterwards: the
// packet has an operation for it whose encoding was not confirmed against the
// decompiled source, and this server does not send a packet it has not read.
type bossBar struct {
	id       [16]byte
	name     string
	progress float32
	color    int32
	overlay  int32
}

// Boss bar operations, in the order the packet's enum declares them.
const (
	bossAdd      = 0
	bossRemove   = 1
	bossProgress = 2
)

// Boss bar colours, in the order BossBarColor declares them.
var bossColors = map[string]int32{
	"pink": 0, "blue": 1, "red": 2, "green": 3, "yellow": 4, "purple": 5, "white": 6,
}

// Boss bar overlays.
var bossOverlays = map[string]int32{
	"progress": 0, "notched_6": 1, "notched_10": 2, "notched_12": 3, "notched_20": 4,
}

// barID is a stable id for a bar's name, so the same name is the same bar
// across restarts of a command's use.
func barID(name string) [16]byte {
	sum := sha256.Sum256([]byte("mcgo:bossbar:" + name))
	var id [16]byte
	copy(id[:], sum[:16])
	return id
}

// addBossBar creates a bar, or replaces the one with that name.
func (s *Server) addBossBar(name, display, color, overlay string) bool {
	c, ok := bossColors[color]
	if !ok {
		return false
	}
	o, ok := bossOverlays[overlay]
	if !ok {
		return false
	}
	bar := &bossBar{id: barID(name), name: display, progress: 0, color: c, overlay: o}

	s.bossMu.Lock()
	if s.bars == nil {
		s.bars = map[string]*bossBar{}
	}
	s.bars[name] = bar
	s.bossMu.Unlock()

	s.broadcastBossAdd(bar)
	return true
}

// removeBossBar takes a bar away.
func (s *Server) removeBossBar(name string) bool {
	s.bossMu.Lock()
	bar, ok := s.bars[name]
	if ok {
		delete(s.bars, name)
	}
	s.bossMu.Unlock()
	if !ok {
		return false
	}
	s.broadcastBossRemove(bar)
	return true
}

// setBossProgress moves a bar's progress, which is 0 to 1.
func (s *Server) setBossProgress(name string, progress float32) bool {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	s.bossMu.Lock()
	bar, ok := s.bars[name]
	if ok {
		bar.progress = progress
	}
	s.bossMu.Unlock()
	if !ok {
		return false
	}
	s.broadcastBossProgress(bar)
	return true
}

// broadcastBossAdd sends a bar to everyone.
func (s *Server) broadcastBossAdd(bar *bossBar) {
	for _, sess := range s.sessionList() {
		_ = sess.sendQuiet(sess.t.PlayClientboundBossEvent, bossAddBody(bar))
	}
}

// broadcastBossRemove takes a bar away everywhere.
func (s *Server) broadcastBossRemove(bar *bossBar) {
	for _, sess := range s.sessionList() {
		_ = sess.sendQuiet(sess.t.PlayClientboundBossEvent, bossRemoveBody(bar.id))
	}
}

// broadcastBossProgress updates one bar's progress everywhere.
func (s *Server) broadcastBossProgress(bar *bossBar) {
	for _, sess := range s.sessionList() {
		_ = sess.sendQuiet(sess.t.PlayClientboundBossEvent, bossProgressBody(bar))
	}
}

// bossAddBody is: the bar's id, the ADD operation, the name component, the
// progress, the colour, the overlay, and the flags byte — darken, music, fog.
func bossAddBody(bar *bossBar) []byte {
	var w mcproto.Writer
	w.WriteBytes(bar.id[:])
	w.WriteVarInt(bossAdd)
	w.WriteBytes(nbtText(bar.name))
	w.WriteFloat(bar.progress)
	w.WriteVarInt(bar.color)
	w.WriteVarInt(bar.overlay)
	w.WriteByte(0) // no darkening, no music, no fog
	return w.Bytes()
}

func bossRemoveBody(id [16]byte) []byte {
	var w mcproto.Writer
	w.WriteBytes(id[:])
	w.WriteVarInt(bossRemove)
	return w.Bytes()
}

func bossProgressBody(bar *bossBar) []byte {
	var w mcproto.Writer
	w.WriteBytes(bar.id[:])
	w.WriteVarInt(bossProgress)
	w.WriteFloat(bar.progress)
	return w.Bytes()
}

// sendBossBars gives a joining player every bar.
func (s *Server) sendBossBars(sess *session) {
	s.bossMu.Lock()
	bars := make([]*bossBar, 0, len(s.bars))
	for _, bar := range s.bars {
		bars = append(bars, bar)
	}
	s.bossMu.Unlock()
	for _, bar := range bars {
		_ = sess.sendQuiet(sess.t.PlayClientboundBossEvent, bossAddBody(bar))
	}
}

// runBossbar is the /bossbar command.
func (s *session) runBossbar(fields []string) string {
	if len(fields) < 2 {
		return "Usage: /bossbar <add|remove|set|list> ..."
	}
	switch fields[1] {
	case "list":
		s.srv.bossMu.Lock()
		defer s.srv.bossMu.Unlock()
		if len(s.srv.bars) == 0 {
			return "No boss bars"
		}
		var names []string
		for name := range s.srv.bars {
			names = append(names, name)
		}
		return "Boss bars: " + strings.Join(names, ", ")

	case "add":
		if len(fields) < 4 {
			return "Usage: /bossbar add <id> <name> [colour] [style]"
		}
		color, overlay := "white", "progress"
		if len(fields) > 4 {
			color = fields[4]
		}
		if len(fields) > 5 {
			overlay = fields[5]
		}
		if !s.srv.addBossBar(fields[2], fields[3], color, overlay) {
			return "Unknown colour or style: " + color + " " + overlay
		}
		return "Added boss bar " + fields[2]

	case "remove":
		if len(fields) < 3 {
			return "Usage: /bossbar remove <id>"
		}
		if !s.srv.removeBossBar(fields[2]) {
			return "No boss bar called " + fields[2]
		}
		return "Removed boss bar " + fields[2]

	case "set":
		if len(fields) < 5 || fields[3] != "value" {
			return "Usage: /bossbar set <id> value <0..1>"
		}
		value, err := strconv.ParseFloat(fields[4], 32)
		if err != nil {
			return "Not a number: " + fields[4]
		}
		if !s.srv.setBossProgress(fields[2], float32(value)) {
			return "No boss bar called " + fields[2]
		}
		return "Set " + fields[2] + " to " + strconv.FormatFloat(value, 'g', -1, 32)
	}
	return "Unknown bossbar command: " + fields[1]
}
