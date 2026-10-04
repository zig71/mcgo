package server

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/proto/v772"
	"mcgo/internal/proto/v776"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// Config holds the advertised server settings. The version is derived from the
// supported protocol tables, not configured.
type Config struct {
	Addr           string
	MOTD           string
	MaxPlayers     int
	WorldPath      string
	PlayersPath    string
	SignsPath      string
	BannersPath    string
	ContainersPath string
	// RegionDir is a saved world's directory — the one containing region/ —
	// e.g. <world>/dimensions/minecraft/overworld. Empty means the flat world.
	RegionDir string
	// NoBackground builds the server without its autosave and tick loops. A
	// test that drives ticks itself has to stop them, or its own passes race
	// the tick loop for the fluid queue and the outcome depends on timing —
	// which is exactly how a fluid test flaked.
	NoBackground bool
}

func DefaultConfig() Config {
	return Config{
		Addr:           ":25565",
		MOTD:           "mcgo",
		MaxPlayers:     20,
		WorldPath:      "world.json",
		PlayersPath:    "players.json",
		SignsPath:      "signs.json",
		BannersPath:    "banners.json",
		ContainersPath: "containers.json",
	}
}

type Server struct {
	cfg      Config
	ln       net.Listener
	versions map[int32]proto.Tables
	newest   proto.Tables

	mu     sync.Mutex
	worlds map[int32]*world.World
	// blockStateTables caches each version's block-state lookup that placement
	// needs to turn properties into a state id.
	blockStateTables map[int32]*world.BlockStateTable
	edits            *world.Edits
	players          *world.Players
	signs            *world.Signs
	banners          *world.Banners
	containers       *world.Containers

	// One clock and one tick loop for the whole server: time is a property of
	// the world, not of a connection. Sessions subscribe while in the play
	// state and are broadcast to from here.
	sessMu   sync.Mutex
	sessions map[*session]struct{}
	age      atomic.Int64
	// stats are the statistics this server counts: per player, by kind and
	// value.
	statMu sync.Mutex
	stats  map[[16]byte]map[string]map[string]int32

	// bars are the boss bars, by name.
	bossMu sync.Mutex
	bars   map[string]*bossBar

	// The scoreboard: objectives, the scores in them, and which one the sidebar
	// shows. Guarded because commands change it while the tick loop reads it.
	scoreMu sync.Mutex
	board   *scoreboard
	sidebar string
	// deaths counts each player's deaths for the sidebar criteria.
	deaths map[string]int32

	// rules are the game rules the server acts on, guarded because the tick
	// loop reads them while a command may change one. frozenTime is the clock's
	// last value, which is what doDaylightCycle holds still.
	rulesMu    sync.Mutex
	rules      gameRules
	frozenTime atomic.Int64

	// dayOffset shifts the clock so /time can set the time of day, and weather
	// is the current weather state, both replayable to a joining client.
	dayOffset atomic.Int64
	weather   atomic.Int32
	// difficulty is 0 peaceful, 1 easy, 2 normal, 3 hard.
	difficulty atomic.Int32
	// Water: the cells to re-evaluate, the per-version level states, and the
	// mutex guarding the first of those (the tick loop and sessions both touch
	// it).
	fluidMu    sync.Mutex
	fluidCells map[[3]int32]bool
	// lavaCells is the same for lava. The two are separate because they tick on
	// different schedules, and one queue shared between them starves whichever
	// ticks more slowly.
	lavaCells map[[3]int32]bool

	// openCounts is how many players have each container block open: a chest
	// lid's block event carries that count, and the lid shuts only at zero.
	animMu      sync.Mutex
	openCounts  map[[3]int32]int32
	fluidStates map[fluidKey]*fluidLevels
	// tagSets are block tag memberships, built once per tag per version: a
	// dig asks whether its tool covers a block on every tick it runs.
	tagSets map[tagKey]map[int32]bool
	// fluidsSeeded records whether the fluids saved in the world file have been
	// put back in the tick queue, which has to happen once after a restart.
	fluidsSeeded bool
	// ents holds the simulated entities; the tick loop and sessions both touch
	// it, hence its own mutex.
	entMu     sync.Mutex
	ents      map[int32]*Entity
	entitySeq atomic.Int32
	stop      chan struct{}
}

func New(cfg Config) *Server {
	s := &Server{
		cfg:        cfg,
		versions:   make(map[int32]proto.Tables, 3),
		worlds:     make(map[int32]*world.World, 3),
		edits:      world.NewEdits(),
		players:    world.NewPlayers(),
		signs:      world.NewSigns(),
		banners:    world.NewBanners(),
		containers: world.NewContainers(),
		sessions:   make(map[*session]struct{}),
		stop:       make(chan struct{}),
		rules:      defaultGameRules(),
		board:      newScoreboard(),
	}
	for _, t := range []proto.Tables{v777.Tables, v776.Tables, v772.Tables} {
		s.versions[t.Protocol] = t
	}
	s.newest = v777.Tables
	s.entitySeq.Store(1) // entity id 1 is each client's own player
	if err := world.EnsureDir(cfg.WorldPath); err != nil {
		log.Printf("world dir: %v", err)
	}
	if err := s.edits.Load(cfg.WorldPath); err != nil {
		log.Printf("world load failed: %v", err)
	} else {
		log.Printf("loaded %d block edits from %s", s.edits.Count(), cfg.WorldPath)
	}
	if err := s.players.Load(cfg.PlayersPath); err != nil {
		log.Printf("players load failed: %v", err)
	} else if n := s.players.Count(); n > 0 {
		log.Printf("loaded %d player states from %s", n, cfg.PlayersPath)
	}
	if err := s.banners.Load(cfg.BannersPath); err != nil {
		log.Printf("banners load failed: %v", err)
	} else if n := s.banners.Count(); n > 0 {
		log.Printf("loaded %d banners from %s", n, cfg.BannersPath)
	}
	if err := s.signs.Load(cfg.SignsPath); err != nil {
		log.Printf("signs load failed: %v", err)
	} else if n := s.signs.Count(); n > 0 {
		log.Printf("loaded %d signs from %s", n, cfg.SignsPath)
	}
	if err := s.containers.Load(cfg.ContainersPath); err != nil {
		log.Printf("containers load failed: %v", err)
	} else if n := s.containers.Count(); n > 0 {
		log.Printf("loaded %d containers from %s", n, cfg.ContainersPath)
	}
	if !cfg.NoBackground {
		go s.autosave()
		go s.tickLoop()
	}
	return s
}

// register subscribes a session to world broadcasts.
func (s *Server) register(sess *session) {
	s.sessMu.Lock()
	s.sessions[sess] = struct{}{}
	s.sessMu.Unlock()
}

// sessionList snapshots the sessions currently in the play state.
func (s *Server) sessionList() []*session {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	out := make([]*session, 0, len(s.sessions))
	for o := range s.sessions {
		out = append(out, o)
	}
	return out
}

// playerInfoBody builds a Player Info Update body for one player. The actions
// are add, game mode, listed and latency: together that is everything the tab
// list needs, and no profile properties (skins) are sent.
func playerInfoBody(uuid [16]byte, name string) []byte {
	var w mcproto.Writer
	w.WriteByte(0x01 | 0x04 | 0x08 | 0x10)
	w.WriteVarInt(1) // one entry
	w.WriteBytes(uuid[:])
	// add_player: the game profile here is name + properties. It carries no
	// UUID of its own — the entry's UUID above is the only one.
	w.WriteString(name)
	w.WriteVarInt(0)  // profile properties
	w.WriteVarInt(1)  // game mode: creative
	w.WriteBool(true) // listed in the tab list
	w.WriteVarInt(0)  // latency
	return w.Bytes()
}

// angleByte converts degrees to the protocol's 1/256-turn angle byte.
func angleByte(deg float32) byte {
	return byte(int32(deg * 256.0 / 360.0))
}

// addEntityBody builds an Add Entity body for a player entity.
//
// Field order and encoding are load-bearing: the velocity comes *before* the
// rotation bytes, and it uses the low-precision Vec3 codec (LpVec3), which
// writes a single zero byte when the entity is not moving. Writing three zero
// shorts instead — and after the rotations — made the packet five bytes longer
// than the client expected, and the client rejects a packet with trailing
// bytes.
func addEntityBody(entityID int32, uuid [16]byte, typeID int32, x, y, z float64, yaw, pitch float32) []byte {
	var w mcproto.Writer
	w.WriteVarInt(entityID)
	w.WriteBytes(uuid[:])
	w.WriteVarInt(typeID)
	w.WriteDouble(x)
	w.WriteDouble(y)
	w.WriteDouble(z)
	w.WriteByte(0) // movement: LpVec3 zero, one byte
	w.WriteByte(angleByte(pitch))
	w.WriteByte(angleByte(yaw))
	w.WriteByte(angleByte(yaw)) // head yaw
	w.WriteVarInt(0)            // object data
	return w.Bytes()
}

// moveEntityBody builds a Move Entity (Pos Rot) body: the position delta in
// the VecDelta linear form, then the rotation bytes. The on-ground flag and
// step count share one VarInt ahead of the delta — writing a boolean there
// shifted every following field.
func moveEntityBody(entityID int32, dx, dy, dz int16, yaw, pitch float32) []byte {
	var w mcproto.Writer
	w.WriteVarInt(entityID)
	w.WriteVarInt(1) // onGround=true, stepCount=0
	w.WriteShort(dx)
	w.WriteShort(dy)
	w.WriteShort(dz)
	w.WriteByte(angleByte(yaw))
	w.WriteByte(angleByte(pitch))
	return w.Bytes()
}

// viewersAt runs fn for every player who can see a point. Entity packets go to
// the players an entity is near, not to the whole server: a player on the far
// side of the world has no business receiving a dropped item's position.
//
// Vanilla tracks this per chunk; the approximation here is a distance, which
// is safe because the chunks in that radius have been sent.
func (s *Server) viewersAt(x, y, z float64, fn func(*session)) {
	for _, sess := range s.sessionList() {
		if sess.seesPos(x, y, z) {
			fn(sess)
		}
	}
}

// broadcastChat sends a player's message to every session as Disguised Chat:
// a player message that carries no signature, which is the right shape for a
// server that does not do chat signing. chat type 0 is minecraft:chat, the
// first entry of the synchronized chat_type registry.
func (s *Server) broadcastChat(sender string, msg string) {
	body := disguisedChatBody(msg, sender)
	for _, sess := range s.sessionList() {
		sess.sendQuiet(sess.t.PlayClientboundDisguisedChat, body)
	}
}

// disguisedChatBody builds a Disguised Chat body: the message component, then
// the chat type as a ChatType.Bound.
//
// The chat type goes through the holder codec, which writes registry index + 1
// and reserves 0 for "the value is spelled out inline". Writing 0 here made
// the client try to read an inline ChatType out of the message text, and it
// rejected the packet. minecraft:chat is index 0, so the wire value is 1.
func disguisedChatBody(msg, sender string) []byte {
	var w mcproto.Writer
	w.WriteBytes(nbtText(msg))
	w.WriteVarInt(1) // chat type: minecraft:chat, referenced by index+1
	w.WriteBytes(nbtText(sender))
	w.WriteBool(false) // no target name
	return w.Bytes()
}

// broadcastEntityRemove despawns an entity for every session.
func (s *Server) broadcastEntityRemove(entityID int32) {
	for _, sess := range s.sessionList() {
		var w mcproto.Writer
		w.WriteVarInt(1)
		w.WriteVarInt(entityID)
		sess.sendQuiet(sess.t.PlayClientboundRemoveEntities, w.Bytes())
	}
}

// broadcastEntityMove tells every other session where a player now is. Deltas
// are in 1/4096 blocks, and nothing is sent when nothing moved.
func (s *Server) broadcastEntityMove(m *session) {
	if !m.hasLast {
		return
	}
	dx := int16((m.posX - m.lastX) * 4096)
	dy := int16((m.posY - m.lastY) * 4096)
	dz := int16((m.posZ - m.lastZ) * 4096)
	if dx == 0 && dy == 0 && dz == 0 &&
		angleByte(m.yaw) == angleByte(m.lastYaw) && angleByte(m.pitch) == angleByte(m.lastPitch) {
		return
	}
	m.lastX, m.lastY, m.lastZ = m.posX, m.posY, m.posZ
	m.lastYaw, m.lastPitch = m.yaw, m.pitch

	for _, o := range s.sessionList() {
		if o == m {
			continue
		}
		o.sendQuiet(o.t.PlayClientboundMoveEntityPosRot, moveEntityBody(m.entityID, dx, dy, dz, m.yaw, m.pitch))

		var h mcproto.Writer
		h.WriteVarInt(m.entityID)
		h.WriteByte(angleByte(m.yaw))
		o.sendQuiet(o.t.PlayClientboundRotateHead, h.Bytes())
	}
}

// broadcastPlayerRemove tells every remaining session that a player left.
func (s *Server) broadcastPlayerRemove(uuid [16]byte) {
	for _, sess := range s.sessionList() {
		var w mcproto.Writer
		w.WriteVarInt(1)
		w.WriteBytes(uuid[:])
		sess.sendQuiet(sess.t.PlayClientboundPlayerInfoRemove, w.Bytes())
	}
}

// broadcastSign sends a sign's block entity data to every session, each with
// its own version's block entity type id.
func (s *Server) broadcastSign(x, y, z int32, text world.SignText) {
	s.sessMu.Lock()
	subs := make([]*session, 0, len(s.sessions))
	for sess := range s.sessions {
		subs = append(subs, sess)
	}
	s.sessMu.Unlock()
	for _, sess := range subs {
		var w mcproto.Writer
		w.WriteLong(packPosition(int(x), int(y), int(z)))
		w.WriteVarInt(sess.t.SignBlockEntityType)
		w.WriteBytes(world.SignNBT(text)) // optional tag: type byte then payload
		if err := sess.send(sess.t.PlayClientboundBlockEntityData, w.Bytes()); err != nil {
			continue
		}
	}
}

func (s *Server) unregister(sess *session) {
	s.sessMu.Lock()
	delete(s.sessions, sess)
	s.sessMu.Unlock()
}

// tickLoop advances the world clock at 20 Hz and broadcasts it once a second.
// Every subsystem that needs a heartbeat hangs off this one loop.
func (s *Server) tickLoop() {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.cookFurnaces()
			s.tickFluids()
			s.tickEntities()
			for _, sess := range s.sessionList() {
				if sess.hurtTicks > 0 {
					sess.hurtTicks--
				}
				sess.tickHazards()
				// A dig in progress is what other players watch: its cracking
				// and its sound are timed from here.
				sess.tickDig()
			}
			age := s.age.Add(1)
			if age%20 != 0 {
				continue
			}
			s.sessMu.Lock()
			subs := make([]*session, 0, len(s.sessions))
			for sess := range s.sessions {
				subs = append(subs, sess)
			}
			s.sessMu.Unlock()
			for _, sess := range subs {
				sess.sendTime(age)
			}
		}
	}
}

// Age reports the world age in ticks.
func (s *Server) Age() int64 { return s.age.Load() }

// autosave flushes edits periodically, so a hard kill loses at most one tick.
func (s *Server) autosave() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		if err := s.edits.Save(s.cfg.WorldPath); err != nil {
			log.Printf("world save failed: %v", err)
		}
		if err := s.players.Save(s.cfg.PlayersPath); err != nil {
			log.Printf("players save failed: %v", err)
		}
		if err := s.signs.Save(s.cfg.SignsPath); err != nil {
			log.Printf("signs save failed: %v", err)
		}
		if err := s.banners.Save(s.cfg.BannersPath); err != nil {
			log.Printf("banners save failed: %v", err)
		}
		if err := s.containers.Save(s.cfg.ContainersPath); err != nil {
			log.Printf("containers save failed: %v", err)
		}
	}
}

// Shutdown stops the tick loop and flushes world edits.
func (s *Server) Shutdown() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	if err := s.edits.Save(s.cfg.WorldPath); err != nil {
		log.Printf("world save on shutdown failed: %v", err)
	}
	if err := s.players.Save(s.cfg.PlayersPath); err != nil {
		log.Printf("players save on shutdown failed: %v", err)
	}
	if err := s.signs.Save(s.cfg.SignsPath); err != nil {
		log.Printf("signs save on shutdown failed: %v", err)
	}
	if err := s.banners.Save(s.cfg.BannersPath); err != nil {
		log.Printf("banners save on shutdown failed: %v", err)
	}
	if err := s.containers.Save(s.cfg.ContainersPath); err != nil {
		log.Printf("containers save on shutdown failed: %v", err)
	}
}

// session is one connection's state, bound to the wire table chosen from the
// client's handshake protocol.
type session struct {
	srv        *Server
	c          *mcproto.Conn
	t          proto.Tables
	world      *world.World
	clockID    int32
	maxPlayers int
	name       string
	uuid       [16]byte
	heldSlot   int32
	// inv is the player's 36 storage slots (0-26 main, 27-35 hotbar), kept in
	// window order so it can be sent verbatim.
	inv     [36]world.Item
	ignored map[int32]bool

	// Open container window, if any.
	containerOpen  bool
	containerPos   [3]int32
	containerState int32
	// containerSize is how many slots the open menu's container half has. It
	// varies by menu: 27 for a chest or shulker box, 5 for a hopper, 3 for a
	// furnace, 10 for a crafting table.
	containerSize int32
	// containerKind is the menu-registry ID of the open menu, which is what
	// decides how a click is interpreted (slot restrictions, result slots).
	containerKind int32
	// containerTitle is the menu's title translation key. It says which
	// machine a three-slot menu is: a furnace cooks, an anvil renames.
	containerTitle string

	// lecternPage is the page an open lectern screen is showing, and
	// lecternPages how many the book has (0 when it could not be read, in which
	// case the page is only clamped at zero).
	lecternPage  int32
	lecternPages int32

	// carried is the stack held on the cursor while a window is open.
	carried world.Item
	// anvilName is the name typed into an open anvil, and anvilCost the
	// experience cost the client is showing for it.
	anvilName string
	anvilCost int32

	// health, dead and hurtTicks are the player's condition. The on-ground
	// flag and the fall it started from are what fall damage is worked out
	// from, since the client is the one that reports touching the ground.
	health float32

	// fireTicks counts down while the player is burning: lava lights them for
	// fifteen seconds and being in water puts them out.
	fireTicks int
	dead      bool
	// gameMode is 0 for survival and 1 for creative, and invulnerable is that
	// mode's immunity: the same flag the abilities packet sends. They are set
	// together, by setGameMode, so they cannot disagree.
	gameMode     int32
	invulnerable bool
	hurtTicks    int32
	onGround     bool
	falling      bool
	fallFrom     float64
	// joinAge is the world age when this player joined, which is what their
	// play time is measured from.
	joinAge int64

	// The dig in progress, tracked the way ServerPlayerGameMode does: which
	// block, the tick it started on, the face the cracking shows, and the last
	// stage sent to the onlookers.
	digActive bool
	digPos    [3]int32
	digStart  int64
	digDir    int32
	digStage  int32
	// bowDrawing is whether a bow is being drawn, and bowDraw the tick it
	// started: the release turns the two into the shot's strength. A bool
	// rather than a zero tick, because the world age *is* zero when a server
	// starts, so "started at 0" and "not drawing" would be the same value.
	bowDrawing bool
	bowDraw    int64

	// spawnX/Y/Z is where a respawn puts the player.
	spawnX, spawnY, spawnZ float64

	// sneaking and sprinting are what other players see of how this one is
	// posed, sent as entity metadata.
	sneaking  bool
	sprinting bool
	// skinParts is which of the player's skin layers to draw, from the client
	// information packet they send while configuring.
	skinParts     byte
	haveSkinParts bool
	// winMu guards the open window: the session's own packet loop mutates it,
	// and the cooking tick loop pushes updates into it for a furnace.
	winMu sync.Mutex

	// In-world presence: this player's entity id, and the last position
	// broadcast to others so unchanged frames cost nothing.
	entityID            int32
	lastX, lastY, lastZ float64
	lastYaw, lastPitch  float32
	hasLast             bool

	// Position and orientation, tracked from movement packets so they can be
	// restored on reconnect.
	uuidKey          string
	posX, posY, posZ float64
	yaw, pitch       float32

	// Chunk streaming: which chunks this client currently holds, and the centre
	// they were computed for.
	sent       map[[2]int32]bool
	curCX      int32
	curCZ      int32
	haveChunks bool
}

// viewRadius is the chunk radius streamed around a player. The same value is
// advertised as the view distance, so the client never expects chunks that
// were not sent (which it would render as void).
const viewRadius = 5

func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	log.Printf("mcgo listening on %s (protocols %d/%d/%d, newest %s)",
		ln.Addr(), v772.Tables.Protocol, v776.Tables.Protocol, v777.Tables.Protocol, s.newest.VersionName)
	for {
		nc, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(nc)
	}
}

// worldFor builds (once per protocol) the world for that version.
func (s *Server) worldFor(t proto.Tables) *world.World {
	// Built before the lock below: it resolves the fluid states, which take the
	// same lock, and taking it twice in one goroutine is a deadlock.
	//
	// The light cost of a state comes from the game's own table, dumped per
	// version (tools/gen_light.py): it is computed from per-state shapes, so the
	// block report cannot carry it.
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.worlds[t.Protocol]; ok {
		return w
	}

	w := world.NewWorld(world.FlatConfig{
		Air:                 t.BlockAir,
		Bedrock:             t.BlockBedrock,
		Dirt:                t.BlockDirt,
		Grass:               t.BlockGrass,
		Stone:               t.BlockStone,
		Biome:               registryID(t.RegistryEntries["minecraft:worldgen/biome"], "minecraft:plains"),
		MinY:                -64,
		Height:              384,
		SurfaceY:            64,
		SignBlockEntityType: t.SignBlockEntityType,
		BlockEntityState:    blockEntityStates(t),
		Emission:            t.BlockEmission,
		LightOpaque:         t.LightOpaque,
		LightDampening:      t.LightDampening,
		LightEmission:       t.LightEmission,
	}, s.edits, s.signs, s.banners)
	if rw := s.regionWorld(t); rw != nil {
		w.UseRegion(rw)
	}
	s.worlds[t.Protocol] = w
	return w
}

// blockEntityStates resolves every state that has a block entity to the id and
// name that block entity is sent with. A chunk has to list one for each such
// block, so this is what keeps a placed chest from turning invisible when its
// chunk is streamed again.
func blockEntityStates(t proto.Tables) map[int32]world.BlockEntityType {
	out := make(map[int32]world.BlockEntityType, len(t.BlockEntityForState))
	for state, name := range t.BlockEntityForState {
		id, ok := t.BlockEntityTypes[name]
		if !ok {
			continue
		}
		out[state] = world.BlockEntityType{ID: id, Name: name}
	}
	return out
}

// regionWorld opens the configured saved world for one protocol version, or
// returns nil when none is configured. Chunks the world lacks still come from
// the flat builder, so an ungenerated area is terrain rather than void.
func (s *Server) regionWorld(t proto.Tables) *world.RegionWorld {
	if s.cfg.RegionDir == "" {
		return nil
	}
	table, err := world.LoadBlockStates(t.BlockStates)
	if err != nil {
		log.Printf("world: block state table: %v", err)
		return nil
	}
	names := t.RegistryEntries["minecraft:worldgen/biome"]
	biomes := make(map[string]int32, len(names))
	for i, name := range names {
		biomes[name] = int32(i)
	}
	return world.OpenRegionWorld(s.cfg.RegionDir, world.ChunkContext{
		Table:            table,
		Biomes:           biomes,
		BlockEntityTypes: t.BlockEntityTypes,
		MinY:             -64,
		Sections:         24,
	})
}

func (s *Server) handle(nc net.Conn) {
	defer nc.Close()

	c := mcproto.NewConn(nc)
	hs, err := c.ReadPacket()
	if err != nil || hs.ID != 0 {
		return
	}
	r := mcproto.NewReader(hs.Data)
	protocol, err := r.ReadVarInt()
	if err != nil {
		return
	}
	if _, err := r.ReadString(); err != nil { // server address
		return
	}
	if _, err := r.ReadUShort(); err != nil { // server port
		return
	}
	next, err := r.ReadVarInt()
	if err != nil {
		return
	}

	// Status is version-independent: always answer, advertising the newest.
	if next == 1 {
		s.handleStatus(c)
		return
	}

	t, ok := s.versions[protocol]
	if !ok {
		// Login-state packet 0x00 is the disconnect, and its id has not moved in
		// any supported version. Saying why matters: a bare socket close is what
		// a client renders as "Network Protocol Error", with nothing to identify
		// the cause — not even which version the client sent.
		var w mcproto.Writer
		w.WriteString(fmt.Sprintf(
			`{"text":"This server speaks 26.3 (protocol %d), 26.2 (%d) and 1.21.8 (%d). Your client sent protocol %d."}`,
			v777.Tables.Protocol, v776.Tables.Protocol, v772.Tables.Protocol, protocol))
		if err := c.WritePacket(0, w.Bytes()); err != nil {
			log.Printf("unsupported protocol %d: disconnect failed: %v", protocol, err)
		}
		log.Printf("unsupported client protocol %d (%s) - disconnected with a reason", protocol, nc.RemoteAddr())
		return
	}
	sess := &session{
		srv:        s,
		c:          c,
		t:          t,
		world:      s.worldFor(t),
		maxPlayers: s.cfg.MaxPlayers,
		// Entity ids come from one counter, so a player never collides with
		// the mobs and items spawned around them.
		entityID:     s.entitySeq.Add(1),
		health:       maxHealth,
		gameMode:     gameModeCreative,
		invulnerable: true,
	}
	sess.handleLogin(protocol)
}
