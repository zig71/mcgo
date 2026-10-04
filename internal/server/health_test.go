package server

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// tcpPair is a connected pair of sockets on the loopback interface.
func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer ln.Close()
	type accepted struct {
		c   net.Conn
		err error
	}
	acc := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		acc <- accepted{c, err}
	}()
	clientEnd, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	a := <-acc
	if a.err != nil {
		t.Fatalf("accepting: %v", a.err)
	}
	return clientEnd, a.c
}

// pipedSession is a session with a live connection and a reader for what the
// server sends it, which is what the paths that talk to a player need.
// packetLog records the ids a session was sent. A single call can push a
// hundred chunk packets through, so the channel below cannot be the only
// record: the log never drops anything.
type packetLog struct {
	mu  sync.Mutex
	ids map[int32]bool
}

func (l *packetLog) add(id int32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ids == nil {
		l.ids = map[int32]bool{}
	}
	l.ids[id] = true
}

func (l *packetLog) has(id int32) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ids[id]
}

func (l *packetLog) seen() []int32 {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]int32, 0, len(l.ids))
	for id := range l.ids {
		out = append(out, id)
	}
	return out
}

func pipedSession(t *testing.T, srv *Server) (*session, chan mcproto.Packet, *packetLog) {
	t.Helper()
	// A loopback socket rather than net.Pipe: a pipe has no buffer, so the
	// server's writes block until the reader happens to run, and a test that
	// sends several packets at once deadlocks.
	clientEnd, serverEnd := tcpPair(t)
	t.Cleanup(func() { clientEnd.Close(); serverEnd.Close() })
	s := placer(0, 0)
	s.srv = srv
	// placer builds a world of its own for the tests that only exercise
	// placement; a session that a server is driving has to stand in the
	// server's world, or the entity tick, the fluid tick and the light engine
	// are looking at a different one and never see what the test wrote.
	s.world = srv.worldFor(s.t)
	s.entityID = 11
	s.health = maxHealth
	// A joined player is creative: set both the mode and the immunity it
	// implies, since a session that is left at the zero value would be in
	// survival and would drop what it broke.
	s.gameMode = gameModeCreative
	s.invulnerable = true
	s.name = "Solo"
	s.posX, s.posY, s.posZ = 0.5, 65, 0.5
	s.c = mcproto.NewConn(serverEnd)

	// Big enough that a chunk flood cannot push earlier packets out: the
	// drop-oldest fallback below is for tests that never read at all.
	got := make(chan mcproto.Packet, 4096)
	log := &packetLog{}
	go func() {
		c := mcproto.NewConn(clientEnd)
		for {
			p, err := c.ReadPacket()
			if err != nil {
				return
			}
			log.add(p.ID)
			select {
			case got <- *p:
			default:
				// A test that is not reading right now: drop the oldest so the
				// socket keeps draining and the server never blocks on a full
				// buffer. The id is already in the log.
				select {
				case <-got:
				default:
				}
				select {
				case got <- *p:
				default:
				}
			}
		}
	}()
	return s, got, log
}

// The health packet carries the health, the food level and the saturation.
func TestSetHealthBody(t *testing.T) {
	s := &session{t: v777.Tables, health: 14.5}
	var w mcproto.Writer
	w.WriteFloat(14.5)
	w.WriteVarInt(20)
	w.WriteFloat(5)
	want := w.Bytes()

	clientEnd, serverEnd := tcpPair(t)
	defer clientEnd.Close()
	defer serverEnd.Close()
	s.c = mcproto.NewConn(serverEnd)

	done := make(chan error, 1)
	go func() { done <- s.sendHealth() }()
	c := mcproto.NewConn(clientEnd)
	p, err := c.ReadPacket()
	if err != nil {
		t.Fatalf("reading the health packet: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("sending the health packet: %v", err)
	}
	got := p.Data
	if p.ID != s.t.PlayClientboundSetHealth {
		t.Errorf("packet id %d, want %d", p.ID, s.t.PlayClientboundSetHealth)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("health body\n got %x\nwant %x", got, want)
	}
}

// A drop of more than three blocks hurts, and a small hop does not.
func TestFallDamage(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	s.invulnerable = false // this test is about taking damage
	s.falling = true
	s.fallFrom = 78
	s.onGround = true
	s.posY = 65
	s.trackFall()
	if want := float32(maxHealth - 10); s.health != want {
		t.Errorf("after a 13 block fall the health is %v, want %v", s.health, want)
	}

	fresh, _, _ := pipedSession(t, srv)
	fresh.invulnerable = false
	fresh.falling = true
	fresh.fallFrom = 67
	fresh.onGround = true
	fresh.posY = 65
	fresh.trackFall()
	if fresh.health != maxHealth {
		t.Errorf("a two block hop cost %v health", maxHealth-fresh.health)
	}
}

// Falling twice does not hurt twice, and a dead player is not hurt at all.
func TestDeathStopsDamage(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	s.invulnerable = false // this test is about taking damage
	tab := v777.Tables

	s.hurtPlayer(tab, 5, "minecraft:fall", nil)
	if s.health != maxHealth-5 {
		t.Errorf("health is %v after five damage, want %v", s.health, maxHealth-5)
	}
	// Still immune: a second hit in the same tick does nothing.
	s.hurtPlayer(tab, 5, "minecraft:fall", nil)
	if s.health != maxHealth-5 {
		t.Errorf("the second hit in one tick was taken: health %v", s.health)
	}

	s.hurtTicks = 0
	s.hurtPlayer(tab, 100, "minecraft:explosion", nil)
	if s.health != 0 || !s.dead {
		t.Errorf("overkill left health %v, dead %v", s.health, s.dead)
	}
	s.hurtTicks = 0
	s.hurtPlayer(tab, 5, "minecraft:fall", nil)
	if s.health != 0 {
		t.Errorf("a dead player took more damage: health %v", s.health)
	}
}

// A dead player who clicks respawn comes back at the spawn point with full
// health.
func TestRespawn(t *testing.T) {
	srv := fluidServer()
	s, got, log := pipedSession(t, srv)
	s.spawnX, s.spawnY, s.spawnZ = 0.5, 65, 0.5
	s.dead = true
	s.health = 0
	s.posX, s.posY, s.posZ = 400, 40, 400
	sessionRegisterForTest(srv, s)

	// Action 0 is perform respawn.
	s.handleClientCommand(mcproto.NewReader([]byte{0x00}))

	if s.dead || s.health != maxHealth {
		t.Errorf("after respawning: dead=%v health=%v", s.dead, s.health)
	}
	if s.posX != 0.5 || s.posY != 65 || s.posZ != 0.5 {
		t.Errorf("respawned at (%v,%v,%v), want the spawn point", s.posX, s.posY, s.posZ)
	}
	// Respawn, the teleport that puts the client back, and the health that
	// clears the death screen.
	deadline := time.After(2 * time.Second)
	for !log.has(s.t.PlayClientboundSetHealth) {
		select {
		case <-got:
		case <-deadline:
			t.Fatalf("respawn sent %v, wanted %d and %d",
				log.seen(), s.t.PlayClientboundRespawn, s.t.PlayClientboundSetHealth)
		}
	}
	if !log.has(s.t.PlayClientboundRespawn) {
		t.Errorf("no Respawn packet, so the client stays on the death screen: %v", log.seen())
	}
	if !log.has(s.t.PlayClientboundPlayerPosition) {
		t.Errorf("no position packet after respawning: %v", log.seen())
	}
}

// A mob takes hits, dies, and leaves its drops behind.
func TestMobDiesAndDrops(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	zombie, ok := tab.EntityIDs["minecraft:zombie"]
	if !ok {
		t.Fatal("no zombie in the entity registry")
	}
	s, _, _ := pipedSession(t, srv)
	s.posX, s.posY, s.posZ = 0.5, 65, 0.5
	sessionRegisterForTest(srv, s)

	e := srv.spawnEntity(kindMob, zombie, 1.5, 65, 0.5)
	// A fist does one point at a time; twenty of them is a dead zombie.
	for i := 0; i < 20; i++ {
		e.HurtTicks = 0
		srv.hurtMob(tab, e, 1, s)
	}
	// The zombie is gone and rotten flesh is what it left.
	items, mobs := 0, 0
	for _, it := range srv.entities() {
		switch it.Kind {
		case kindItem:
			items++
		case kindMob:
			mobs++
		}
	}
	if mobs != 0 {
		t.Errorf("%d mobs survived the last hit, want the zombie gone", mobs)
	}
	if items != 1 {
		t.Errorf("%d drops from a zombie, want 1", items)
	}

	// Out of reach, nothing happens.
	far := srv.spawnEntity(kindMob, zombie, 40, 65, 0.5)
	s.handleAttack(mcproto.NewReader(varintBytes(far.ID)))
	if far.HurtTicks != 0 {
		t.Error("a mob forty blocks away was hit")
	}
}

// An explosion hurts whoever is standing in it.
func TestExplosionHurtsPlayers(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	near, _, _ := pipedSession(t, srv)
	near.invulnerable = false // this test is about taking damage
	near.posX, near.posY, near.posZ = 0.5, 65, 0.5
	sessionRegisterForTest(srv, near)
	far, _, _ := pipedSession(t, srv)
	far.invulnerable = false
	far.entityID = 12
	far.posX, far.posY, far.posZ = 40, 65, 40
	sessionRegisterForTest(srv, far)

	srv.damageInBlast(tab, 0.5, 65, 0.5, tntPower)

	if !near.dead {
		t.Errorf("the player at the centre of the blast survived with %v health", near.health)
	}
	if far.health != maxHealth {
		t.Errorf("a player forty blocks away lost %v health", maxHealth-far.health)
	}
}

// varintBytes encodes a varint for a test reader.
func varintBytes(v int32) []byte {
	var w mcproto.Writer
	w.WriteVarInt(v)
	return w.Bytes()
}

// sessionRegisterForTest adds a session to the broadcast list.
func sessionRegisterForTest(s *Server, sess *session) {
	s.sessMu.Lock()
	if s.sessions == nil {
		s.sessions = make(map[*session]struct{})
	}
	s.sessions[sess] = struct{}{}
	s.sessMu.Unlock()
}

// Abilities follow the game mode: creative is invulnerable, can fly and breaks
// instantly; survival is none of those.
func TestAbilitiesFollowGameMode(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	if err := s.setGameMode(gameModeSurvival); err != nil {
		t.Fatalf("changing mode: %v", err)
	}
	p := waitPacket(t, got, s.t.PlayClientboundPlayerAbilities)
	if p.Data[0] != 0 {
		t.Errorf("survival abilities are %#x, want none", p.Data[0])
	}
	if s.invulnerable {
		t.Error("a survival player is still invulnerable")
	}

	if err := s.setGameMode(gameModeCreative); err != nil {
		t.Fatalf("changing back: %v", err)
	}
	if !s.invulnerable || s.gameMode != gameModeCreative {
		t.Errorf("creative mode left the player vulnerable: mode %d invulnerable %v", s.gameMode, s.invulnerable)
	}
}

// A survival player can be hurt, fall, die and respawn: the whole health path,
// which was unreachable while every player was creative.
func TestSurvivalPlayerDiesAndRespawns(t *testing.T) {
	srv := fluidServer()
	s, _, log := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	s.spawnX, s.spawnY, s.spawnZ = 0.5, 65, 0.5

	if err := s.setGameMode(gameModeSurvival); err != nil {
		t.Fatalf("changing mode: %v", err)
	}
	s.falling = true
	s.fallFrom = 120 // a 55 block drop
	s.onGround = true
	s.posY = 65
	s.trackFall()

	if !s.dead {
		t.Fatalf("a 55 block fall left a survival player alive with %v health", s.health)
	}
	waitFor(t, log, s.t.PlayClientboundPlayerCombatKill)
	// The reader goroutine records the log asynchronously, so wait for the
	// message rather than looking once: that check was a race.
	waitFor(t, log, s.t.PlayClientboundSystemChat)

	s.handleClientCommand(mcproto.NewReader([]byte{0x00}))
	if s.dead || s.health != maxHealth {
		t.Errorf("after respawning: dead=%v health=%v", s.dead, s.health)
	}
	if s.posX != 0.5 || s.posY != 65 {
		t.Errorf("respawned at (%v,%v)", s.posX, s.posY)
	}
}

// Standing in lava hurts and lights you on fire, water puts the fire out, and
// lava can kill you. hurtPlayer's own window is what paces the damage to
// vanilla's four every ten ticks, so the test clears it like the tick loop does.
func TestLavaHurts(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	s.invulnerable = false // this test is about taking damage
	tab := v777.Tables
	lv := srv.lavaLevelsFor(tab)
	if lv == nil {
		t.Fatal("the version has no lava states")
	}
	s.world.SetBlock(0, 65, 0, lv.byLevel[waterSource])

	s.tickHazards()
	if want := float32(maxHealth - 4); s.health != want {
		t.Errorf("one tick in lava left %v health, want %v", s.health, want)
	}
	if s.fireTicks != 300 {
		t.Errorf("lava lit %d ticks of fire, want 300", s.fireTicks)
	}

	// The tick loop counts the window down as it goes, so nine more ticks land
	// no second hit: lava hurts four every ten ticks, not four every tick.
	for range 9 {
		// The tick loop counts the window down *before* the hazard, which is
		// the order that lands the tenth-tick hit.
		s.hurtTicks--
		s.tickHazards()
	}
	if want := float32(maxHealth - 4); s.health != want {
		t.Errorf("ten ticks in lava left %v health, want %v", s.health, want)
	}
	// The tenth tick, window and all.
	s.hurtTicks--
	s.tickHazards()
	if want := float32(maxHealth - 8); s.health != want {
		t.Errorf("the tenth tick left %v health, want %v", s.health, want)
	}

	// Water puts the fire out.
	wlv := srv.waterLevelsFor(tab)
	s.world.SetBlock(0, 65, 0, wlv.byLevel[waterSource])
	if name, _, ok := srv.blockStateName(tab, s.world.Block(0, 65, 0)); !ok || name != "minecraft:water" {
		t.Fatalf("the test put %q at the player's feet, not water", name)
	}
	s.hurtTicks = 0
	s.tickHazards()
	if s.fireTicks != 0 {
		t.Errorf("water left %d ticks of fire burning", s.fireTicks)
	}

	// Lava kills.
	s.world.SetBlock(0, 65, 0, lv.byLevel[waterSource])
	for range 200 {
		if s.dead {
			break
		}
		s.hurtTicks = 0
		s.tickHazards()
	}
	if !s.dead {
		t.Errorf("a player stood in lava for ten seconds and lived: %v health", s.health)
	}
}
