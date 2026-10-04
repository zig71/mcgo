package server

import (
	"bytes"
	"strings"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// checkCommandTree decodes a Commands packet the way a client does and returns
// the node count. Every argument type's properties are read at their real
// widths, so a body that is a byte off either runs out of data or leaves
// trailing bytes - which is how a client's "failed to decode packet" shows up in
// a test. The parser ids are the v777 command_argument_type registry's.
func checkCommandTree(t *testing.T, b []byte) int {
	t.Helper()
	r := mcproto.NewReader(b)
	count, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("node count: %v", err)
	}
	type entry struct {
		children    []int32
		redirect    int32
		hasRedirect bool
	}
	entries := make([]entry, 0, count)
	for range count {
		flags, err := r.ReadByte()
		if err != nil {
			t.Fatalf("flags: %v", err)
		}
		n, err := r.ReadVarInt()
		if err != nil {
			t.Fatalf("child count: %v", err)
		}
		e := entry{hasRedirect: flags&8 != 0}
		for range n {
			c, err := r.ReadVarInt()
			if err != nil {
				t.Fatalf("child: %v", err)
			}
			e.children = append(e.children, c)
		}
		if e.hasRedirect { // the redirect index comes before the node's stub
			if e.redirect, err = r.ReadVarInt(); err != nil {
				t.Fatalf("redirect: %v", err)
			}
		}
		switch flags & 3 {
		case 0: // root: no stub
		case 1: // literal: a name
			if _, err := r.ReadString(); err != nil {
				t.Fatalf("literal name: %v", err)
			}
		case 2: // argument: a name, the parser id, then the parser's properties
			if _, err := r.ReadString(); err != nil {
				t.Fatalf("argument name: %v", err)
			}
			parserID, err := r.ReadVarInt()
			if err != nil {
				t.Fatalf("parser id: %v", err)
			}
			skipArgumentProperties(t, r, parserID)
		default:
			t.Fatalf("node %d has type %d", len(entries), flags&3)
		}
		entries = append(entries, e)
	}
	root, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("root index: %v", err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d trailing bytes in the command tree", r.Remaining())
	}
	if int(root) >= len(entries) {
		t.Fatalf("root index %d is past the %d entries", root, len(entries))
	}
	for i, e := range entries {
		for _, c := range e.children {
			if int(c) >= len(entries) {
				t.Fatalf("node %d points at child %d of %d", i, c, len(entries))
			}
		}
		if e.hasRedirect && int(e.redirect) >= len(entries) {
			t.Fatalf("node %d redirects to %d of %d", i, e.redirect, len(entries))
		}
	}
	return int(count)
}

// skipArgumentProperties reads what an argument type writes after its parser id.
// The widths are the serializers': a double bound is eight bytes where a float's
// is four, and getting that wrong is what makes a client reject the packet. Every
// type not listed is a singleton and writes nothing at all.
func skipArgumentProperties(t *testing.T, r *mcproto.Reader, parserID int32) {
	t.Helper()
	skip := func(n int) {
		if _, err := r.ReadBytes(n); err != nil {
			t.Fatalf("parser %d: %v", parserID, err)
		}
	}
	number := func(bound int) {
		flags, err := r.ReadByte()
		if err != nil {
			t.Fatalf("parser %d: %v", parserID, err)
		}
		for bit := byte(1); bit <= 2; bit <<= 1 {
			if flags&bit != 0 {
				skip(bound)
			}
		}
	}
	switch parserID {
	case 0: // brigadier:bool
	case 1: // brigadier:float
		number(4)
	case 2: // brigadier:double
		number(8)
	case 3: // brigadier:integer
		number(4)
	case 4: // brigadier:long
		number(8)
	case 5: // brigadier:string: the string type
		if _, err := r.ReadVarInt(); err != nil {
			t.Fatalf("string type: %v", err)
		}
	case 6, 31: // entity, score_holder: a byte of flags
		if _, err := r.ReadByte(); err != nil {
			t.Fatalf("flags: %v", err)
		}
	case 43: // time: an int minimum
		skip(4)
	case 44, 45, 46, 47, 48: // the resource families: the registry's identifier
		if _, err := r.ReadString(); err != nil {
			t.Fatalf("registry: %v", err)
		}
	}
}

// The tree this server sends is generated (tools/gen_commands.py) and sent
// verbatim in the Commands packet, so it has to decode the way a client does:
// one byte of drift and the client drops the connection with "failed to decode
// packet 'clientbound/minecraft:commands'".
func TestCommandTreeIsGenerated(t *testing.T) {
	body := v777.Tables.CommandsBytes
	if !bytes.Contains(body, []byte("gamemode")) {
		t.Error("the tree does not mention gamemode")
	}
	if n := checkCommandTree(t, body); n < 1000 {
		t.Errorf("the tree holds %d nodes, want the data pack's thousands", n)
	}
}

func TestCommandsReachTheirTargets(t *testing.T) {
	srv := &Server{}
	s := &session{t: v777.Tables, srv: srv, name: "Tester", maxPlayers: 20}

	// /time day moves the clock so that the time of day is 1000.
	age := srv.Age()
	if got := s.runCommand("time day"); got != "Set the time to day" {
		t.Errorf("reply = %q", got)
	}
	if got := (age + srv.dayOffset.Load()) % 24000; got != 1000 {
		t.Errorf("time of day = %d, want 1000", got)
	}

	if got := s.runCommand("weather rain"); got != "Weather set to rain" {
		t.Errorf("reply = %q", got)
	}
	if got := srv.weather.Load(); got != weatherRain {
		t.Errorf("weather = %d, want rain", got)
	}
	s.runCommand("weather thunder")
	if got := srv.weather.Load(); got != weatherThunder {
		t.Errorf("weather = %d, want thunder", got)
	}
	s.runCommand("weather clear")
	if got := srv.weather.Load(); got != weatherClear {
		t.Errorf("weather = %d, want clear", got)
	}

	// Unknown commands and missing arguments must not panic or act.
	if got := s.runCommand("nonsense"); got != "Unknown command: nonsense" {
		t.Errorf("unknown command reply = %q", got)
	}
	for _, cmd := range []string{"time", "weather", "gamemode"} {
		if got := s.runCommand(cmd); got == "" {
			t.Errorf("%q should have explained its usage", cmd)
		}
	}
	// A blank line is not a command, and says nothing.
	if got := s.runCommand("   "); got != "" {
		t.Errorf("blank command replied %q", got)
	}
}

func TestDifficultyCommand(t *testing.T) {
	srv := &Server{}
	s := &session{t: v777.Tables, srv: srv}

	if got := s.runCommand("difficulty hard"); got != "Difficulty set to hard" {
		t.Errorf("reply = %q", got)
	}
	if got := srv.difficulty.Load(); got != 3 {
		t.Errorf("difficulty = %d, want hard (3)", got)
	}
	if s.runCommand("difficulty nonsense") == "" {
		t.Error("an unknown difficulty gave no usage line")
	}
	if s.runCommand("difficulty") == "" {
		t.Error("a bare /difficulty gave no usage line")
	}
}

// A block change is only sent to players close enough to see it, so a session
// on the far side of the world does not get every edit.
func TestBlockChangeReach(t *testing.T) {
	near := &session{posX: 8, posY: 65, posZ: 8}
	if !near.sees(10, 65, 10) {
		t.Error("a block two blocks away was judged out of range")
	}
	far := &session{posX: 1000, posY: 65, posZ: 1000}
	if far.sees(0, 65, 0) {
		t.Error("a block 1400 blocks away was judged in range")
	}
}

func TestWeatherEvents(t *testing.T) {
	// The clear events are STOP_RAINING (2) then thunder level 0.
	clear := weatherEvents(weatherClear)
	if len(clear) != 2 || clear[0].id != 2 || clear[1].id != 8 || clear[1].param != 0 {
		t.Errorf("clear weather events = %+v", clear)
	}
	thunder := weatherEvents(weatherThunder)
	if thunder[0].id != 1 || thunder[1].id != 8 || thunder[1].param != 1 {
		t.Errorf("thunder events = %+v", thunder)
	}
}

// /tp moves the player where it says.
func TestTpCommand(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	if reply := s.runCommand("tp 100 70 -50"); !strings.Contains(reply, "100") {
		t.Errorf("the reply was %q", reply)
	}
	if s.posX != 100 || s.posY != 70 || s.posZ != -50 {
		t.Errorf("teleported to (%v,%v,%v)", s.posX, s.posY, s.posZ)
	}
	if reply := s.runCommand("tp 1 2"); !strings.Contains(reply, "Usage") {
		t.Errorf("a short /tp replied %q", reply)
	}
	if reply := s.runCommand("tp a b c"); !strings.Contains(reply, "Not a number") {
		t.Errorf("a non-numeric /tp replied %q", reply)
	}
}

// /setblock places a block, with or without properties.
func TestSetblockCommand(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	tab := v777.Tables

	if reply := s.runCommand("setblock 1 64 1 minecraft:stone"); !strings.Contains(reply, "Changed") {
		t.Errorf("setting a block replied %q", reply)
	}
	want, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone in the block table")
	}
	if got := s.world.Block(1, 64, 1); got != want {
		t.Errorf("the block is %d, want stone %d", got, want)
	}

	// With properties.
	if reply := s.runCommand("setblock 2 64 1 minecraft:oak_door[facing=north,half=lower,open=true]"); !strings.Contains(reply, "Changed") {
		t.Errorf("setting a door replied %q", reply)
	}
	door, ok := srv.blockState(tab, "minecraft:oak_door", map[string]string{
		"facing": "north", "half": "lower", "open": "true",
	})
	if !ok {
		t.Fatal("no such door state in the table")
	}
	if got := s.world.Block(2, 64, 1); got != door {
		t.Errorf("the door is %d, want %d", got, door)
	}

	if reply := s.runCommand("setblock 1 64 1 not_a_block"); !strings.Contains(reply, "Unknown block") {
		t.Errorf("a bad block replied %q", reply)
	}
	if reply := s.runCommand("setblock 1 64 nope minecraft:stone"); !strings.Contains(reply, "Not a number") {
		t.Errorf("a bad coordinate replied %q", reply)
	}
	if reply := s.runCommand("setblock 1 64 1 minecraft:oak_door[notaproperty=x]"); !strings.Contains(reply, "Unknown block or property") {
		t.Errorf("a bad property replied %q", reply)
	}
}

// parseBlockSpec splits a spec into its name and properties.
func TestParseBlockSpec(t *testing.T) {
	if name, props := parseBlockSpec("minecraft:stone"); name != "minecraft:stone" || props != nil {
		t.Errorf("a plain name gave %q %v", name, props)
	}
	name, props := parseBlockSpec("minecraft:oak_door[facing=east,open=true]")
	if name != "minecraft:oak_door" {
		t.Errorf("name is %q", name)
	}
	if props["facing"] != "east" || props["open"] != "true" || len(props) != 2 {
		t.Errorf("properties are %v", props)
	}
}
