package server

import (
	"fmt"
	"strconv"
	"strings"

	"mcgo/internal/mcproto"
)

// Weather states, as the server stores them. Clear is what the world starts in.
const (
	weatherClear = iota
	weatherRain
	weatherThunder
)

// runCommand runs a "/" command without its slash, and returns the line to send
// back to whoever typed it. An unknown command is answered rather than ignored:
// the tree the client holds offers every vanilla command, so a client will send
// ones this server does not implement.
func (s *session) runCommand(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	cmd, args := fields[0], fields[1:]
	switch cmd {
	case "help":
		return "Commands: help, list, spawn, time <day|noon|night|midnight>, " +
			"weather <clear|rain|thunder>, gamemode <creative|survival>, " +
			"difficulty <peaceful|easy|normal|hard>, tp <x> <y> <z>, " +
			"setblock <x> <y> <z> <block>[properties], gamerule, scoreboard, bossbar, " +
			"summon <entity> [x] [y] [z]"
	case "list":
		return s.playerList()
	case "spawn":
		s.teleportTo(s.spawnX, s.spawnY, s.spawnZ)
		return "Teleported to spawn"
	case "time":
		return s.runTime(args)
	case "weather":
		return s.runWeather(args)
	case "gamemode":
		return s.runGamemode(args)
	case "difficulty":
		return s.runDifficulty(args)
	case "tp":
		return s.runTp(args)
	case "setblock":
		return s.runSetblock(args)
	case "summon":
		return s.runSummon(args)
	case "gamerule":
		return s.runGamerule(args)
	case "scoreboard":
		// These two dispatch on the command name themselves, so they take the
		// whole line rather than the arguments alone.
		return s.runScoreboard(fields)
	case "bossbar":
		return s.runBossbar(fields)
	}
	return "Unknown command: " + cmd
}

// handleChatCommand runs a command the client sent, and answers whoever sent
// it.
//
// Only the command string is read. The rest of the packet — timestamp, salt,
// argument signatures, the acknowledged bitset — differs between the plain and
// the signed form and has changed between versions, and the frame is discarded
// whole, so reading it would only be a way to get it wrong.
func (s *session) handleChatCommand(r *mcproto.Reader) {
	command, err := r.ReadString()
	if err != nil {
		return
	}
	reply := s.runCommand(command)
	if reply == "" {
		return
	}
	_ = s.sendQuiet(s.t.PlayClientboundSystemChat, systemChatBody(reply, false))
}

// playerList is the tab list, by name.
func (s *session) playerList() string {
	names := make([]string, 0, 8)
	for _, other := range s.srv.sessionList() {
		if other.name != "" {
			names = append(names, other.name)
		}
	}
	if len(names) == 0 {
		return "Players: none"
	}
	return "Players (" + strconv.Itoa(len(names)) + "): " + strings.Join(names, ", ")
}

// timeTargets are the times of day /time accepts, in ticks. Vanilla's names:
// day is 1000, noon 6000, night 13000, midnight 18000.
var timeTargets = map[string]int64{"day": 1000, "noon": 6000, "night": 13000, "midnight": 18000}

func (s *session) runTime(args []string) string {
	if len(args) != 1 {
		return "Usage: /time <day|noon|night|midnight>"
	}
	target, ok := timeTargets[args[0]]
	if !ok {
		return "Usage: /time <day|noon|night|midnight>"
	}
	// The clock is the server's age plus an offset, so the offset is what moves
	// the world to the wanted time of day.
	age := s.srv.Age()
	s.srv.dayOffset.Store(((target-age)%24000 + 24000) % 24000)
	for _, other := range s.srv.sessionList() {
		other.sendTime(age)
	}
	return "Set the time to " + args[0]
}

func (s *session) runWeather(args []string) string {
	if len(args) != 1 {
		return "Usage: /weather <clear|rain|thunder>"
	}
	var state int32
	switch args[0] {
	case "clear":
		state = weatherClear
	case "rain":
		state = weatherRain
	case "thunder":
		state = weatherThunder
	default:
		return "Usage: /weather <clear|rain|thunder>"
	}
	s.srv.weather.Store(state)
	for _, other := range s.srv.sessionList() {
		other.sendWeather()
	}
	return "Weather set to " + args[0]
}

func (s *session) runGamemode(args []string) string {
	if len(args) != 1 {
		return "Usage: /gamemode <creative|survival>"
	}
	mode := int32(-1)
	switch args[0] {
	case "creative":
		mode = gameModeCreative
	case "survival":
		mode = gameModeSurvival
	}
	if mode < 0 {
		return "Unknown game mode: " + args[0]
	}
	if err := s.setGameMode(mode); err != nil {
		return "Could not change game mode"
	}
	return "Set game mode to " + args[0]
}

// Difficulties, in vanilla's order: peaceful, easy, normal, hard.
var difficulties = map[string]int32{"peaceful": 0, "easy": 1, "normal": 2, "hard": 3}

func (s *session) runDifficulty(args []string) string {
	if len(args) != 1 {
		return "Usage: /difficulty <peaceful|easy|normal|hard>"
	}
	level, ok := difficulties[args[0]]
	if !ok {
		return "Usage: /difficulty <peaceful|easy|normal|hard>"
	}
	s.srv.difficulty.Store(level)
	for _, other := range s.srv.sessionList() {
		other.sendDifficulty()
	}
	return "Difficulty set to " + args[0]
}

func (s *session) runTp(args []string) string {
	if len(args) != 3 {
		return "Usage: /tp <x> <y> <z>"
	}
	var coords [3]float64
	for i, a := range args {
		v, err := strconv.ParseFloat(a, 64)
		if err != nil {
			return "Not a number: " + a
		}
		coords[i] = v
	}
	s.teleportTo(coords[0], coords[1], coords[2])
	return fmt.Sprintf("Teleported to %.1f %.1f %.1f", coords[0], coords[1], coords[2])
}

func (s *session) runSetblock(args []string) string {
	if len(args) < 4 {
		return "Usage: /setblock <x> <y> <z> <block>[properties]"
	}
	var coords [3]int32
	for i, a := range args[:3] {
		v, err := strconv.ParseInt(a, 10, 32)
		if err != nil {
			return "Not a number: " + a
		}
		coords[i] = int32(v)
	}
	name, props := parseBlockSpec(strings.Join(args[3:], ""))
	state, ok := s.srv.blockState(s.t, name, props)
	if !ok {
		// A name that does not exist reads differently from a state that could
		// not be built out of the properties given.
		if props != nil {
			return "Unknown block or property: " + name
		}
		return "Unknown block: " + name
	}
	s.setBlockUpdate(coords[0], coords[1], coords[2], state)
	return fmt.Sprintf("Changed the block at %d %d %d", coords[0], coords[1], coords[2])
}

// runSummon is /summon <entity> [x] [y] [z].
//
// The command tree the client is sent is vanilla's own and already offers
// summon, so without this a player sees it in the completion list and gets back
// "Unknown command" — worse than not offering it at all. It is also how every
// entity type this server knows can be reached and tried, including the ones no
// spawn egg exists for.
func (s *session) runSummon(args []string) string {
	if len(args) != 1 && len(args) != 4 {
		return "Usage: /summon <entity> [x] [y] [z]"
	}
	// Both "zombie" and "minecraft:zombie" name the same entity, as in vanilla.
	name := strings.TrimPrefix(args[0], "minecraft:")
	typ, ok := s.t.EntityIDs["minecraft:"+name]
	if !ok {
		return "Unknown entity: " + args[0]
	}
	x, y, z := s.posX, s.posY, s.posZ
	if len(args) == 4 {
		coords := make([]float64, 3)
		for i, a := range args[1:] {
			f, err := strconv.ParseFloat(a, 64)
			if err != nil {
				return "Not a number: " + a
			}
			coords[i] = f
		}
		x, y, z = coords[0], coords[1], coords[2]
	}
	// TNT is the one entity with behaviour of its own here; everything else
	// takes the mob path, which is gravity, a walk and a noise.
	kind := kindMob
	if name == "tnt" {
		kind = kindTNT
	}
	e := s.srv.spawnEntity(kind, typ, x, y, z)
	if kind == kindTNT {
		e.Fuse = tntFuse
	}
	return "Summoned " + name
}

func (s *session) runGamerule(args []string) string {
	switch len(args) {
	case 0:
		return s.srv.ruleList()
	case 1:
		value, ok := s.srv.rule(args[0])
		if !ok {
			return "Unknown gamerule: " + args[0]
		}
		return args[0] + " is now set to: " + strconv.FormatBool(value)
	case 2:
		var value bool
		switch args[1] {
		case "true":
			value = true
		case "false":
			value = false
		default:
			return "A gamerule is a boolean: true or false, not " + args[1]
		}
		if !s.srv.setRule(args[0], value) {
			return "Unknown gamerule: " + args[0]
		}
		return args[0] + " is now set to: " + strconv.FormatBool(value)
	}
	return "Usage: /gamerule <name> [true|false]"
}

// parseBlockSpec splits "minecraft:oak_door[facing=north,open=true]" into the
// block's name and its properties. A spec without properties gives nil, which
// is what tells a caller the default state was asked for.
func parseBlockSpec(spec string) (string, map[string]string) {
	open := strings.IndexByte(spec, '[')
	if open < 0 || !strings.HasSuffix(spec, "]") {
		return spec, nil
	}
	name := spec[:open]
	props := make(map[string]string)
	for _, pair := range strings.Split(spec[open+1:len(spec)-1], ",") {
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 {
			return name, props
		}
		props[strings.TrimSpace(pair[:eq])] = strings.TrimSpace(pair[eq+1:])
	}
	return name, props
}

// gameEvent is one clientbound Game Event: the reason, and the float that some
// of them carry.
type gameEvent struct {
	id    int32
	param float32
}

// weatherEvents are the game events one weather state is made of. Vanilla sends
// two of them: start or stop the rain, then set the thunder level.
func weatherEvents(state int32) []gameEvent {
	switch state {
	case weatherRain:
		return []gameEvent{{id: 1}, {id: 7, param: 1}}
	case weatherThunder:
		return []gameEvent{{id: 1}, {id: 8, param: 1}}
	}
	return []gameEvent{{id: 2}, {id: 8, param: 0}}
}

// sendWeather tells this client what the weather is. It is sent on join, and
// again to everyone when the weather changes.
func (s *session) sendWeather() {
	for _, e := range weatherEvents(s.srv.weather.Load()) {
		var w mcproto.Writer
		w.WriteByte(byte(e.id))
		w.WriteFloat(e.param)
		if err := s.sendQuiet(s.t.PlayClientboundGameEvent, w.Bytes()); err != nil {
			return
		}
	}
}

// sendDifficulty tells this client the difficulty, which is one byte plus
// whether it is locked.
func (s *session) sendDifficulty() {
	var w mcproto.Writer
	w.WriteByte(byte(s.srv.difficulty.Load()))
	w.WriteBool(false) // not locked
	_ = s.sendQuiet(s.t.PlayClientboundChangeDifficulty, w.Bytes())
}

// teleportTo moves the player and tells their client, which is the only way the
// client's own position moves: it would otherwise walk straight back.
func (s *session) teleportTo(x, y, z float64) {
	s.posX, s.posY, s.posZ = x, y, z
	var w mcproto.Writer
	w.WriteVarInt(1) // teleport id
	w.WriteDouble(x)
	w.WriteDouble(y)
	w.WriteDouble(z)
	w.WriteDouble(0) // velocity x
	w.WriteDouble(0) // velocity y
	w.WriteDouble(0) // velocity z
	w.WriteFloat(s.yaw)
	w.WriteFloat(s.pitch)
	w.WriteInt(0) // relative flags
	_ = s.sendQuiet(s.t.PlayClientboundPlayerPosition, w.Bytes())
}

// sendCommands describes the command tree to the client, which is what makes
// the commands appear in the chat window's completion list. The whole tree is
// generated by tools/gen_commands.py and sent verbatim.
func (s *session) sendCommands() error {
	return s.send(s.t.PlayClientboundCommands, s.t.CommandsBytes)
}
