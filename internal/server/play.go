package server

import (
	"log"
	"math"
	"strings"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

const dimensionName = "minecraft:overworld"

// send writes a clientbound packet, logging it so a client-side decode failure
// can be traced to the last packet that went out.
// readMoveFlags reads the packed on-ground/horizontal-collision byte that
// every movement packet ends with. Bit 0 is on-ground.
func (s *session) readMoveFlags(r *mcproto.Reader) {
	flags, err := r.ReadByte()
	if err != nil {
		return
	}
	s.onGround = flags&1 != 0
}

func (s *session) send(id int32, data []byte) error {
	log.Printf("-> 0x%02X len=%d", id, len(data))
	return s.c.WritePacket(id, data)
}

// sendQuiet writes without logging: the periodic clock and keep-alive would
// otherwise flood the trace that makes real decode failures findable.
func (s *session) sendQuiet(id int32, data []byte) error {
	return s.c.WritePacket(id, data)
}

// sendTime broadcasts the world clock. It is called from the server tick loop,
// so it must tolerate a session disappearing mid-broadcast.
func (s *session) sendTime(age int64) {
	var tw mcproto.Writer
	tw.WriteLong(age)         // world age
	tw.WriteVarInt(1)         // one clock
	tw.WriteVarInt(s.clockID) // minecraft:overworld
	// Total ticks, which the client takes modulo a day: the offset is what
	// /time moves. With doDaylightCycle off the clock is held still.
	time := (age + s.srv.dayOffset.Load()) % 24000
	if frozen, ok := s.srv.rule("doDaylightCycle"); ok && !frozen {
		time = s.srv.frozenTime.Load()
	} else {
		// Remember it: this is the value a freeze holds on to.
		s.srv.frozenTime.Store(time)
	}
	tw.WriteVarLong(time)
	tw.WriteFloat(0) // partial tick
	tw.WriteFloat(1) // rate
	_ = s.sendQuiet(s.t.PlayClientboundSetTime, tw.Bytes())
}

// enterPlay sends the join sequence and then services the play state.
func (s *session) enterPlay() {
	// Subscribe to the server-wide clock: time belongs to the world, not to
	// this connection.
	s.clockID = registryID(s.t.RegistryEntries["minecraft:world_clock"], dimensionName)
	s.srv.register(s)
	s.entityID = s.srv.entitySeq.Add(1)
	defer s.saveState()
	defer s.srv.broadcastPlayerRemove(s.uuid)
	defer s.srv.broadcastEntityRemove(s.entityID)
	defer s.srv.unregister(s)

	// Player list: the newcomer is told about everyone (itself included), and
	// every other session is told about the newcomer.
	for _, o := range s.srv.sessionList() {
		s.sendQuiet(s.t.PlayClientboundPlayerInfoUpdate, playerInfoBody(o.uuid, o.name))
		if o != s {
			o.sendQuiet(o.t.PlayClientboundPlayerInfoUpdate, playerInfoBody(s.uuid, s.name))
		}
	}
	var tab mcproto.Writer
	tab.WriteBytes(nbtText("mcgo"))
	tab.WriteBytes(nbtText("26.3 · creative"))
	s.sendQuiet(s.t.PlayClientboundTabList, tab.Bytes())

	// Restore what the player left behind: position, hotbar and selection.
	saved, hasSaved := s.srv.players.Get(s.uuidKey)
	if hasSaved {
		s.inv = saved.Inv
		s.heldSlot = saved.HeldSlot
		if saved.HasPos {
			s.posX, s.posY, s.posZ = saved.X, saved.Y, saved.Z
			s.yaw, s.pitch = saved.Yaw, saved.Pitch
		}
	}

	var w mcproto.Writer

	// Login (play).
	w.WriteInt(s.entityID) // entity id, from the same counter as everything else
	w.WriteBool(false)     // is hardcore
	w.WriteVarInt(1)       // dimension names
	w.WriteString(dimensionName)
	w.WriteVarInt(int32(s.maxPlayers))
	w.WriteVarInt(viewRadius) // view distance
	w.WriteVarInt(viewRadius) // simulation distance
	w.WriteBool(false)        // reduced debug info
	w.WriteBool(true)         // enable respawn screen
	w.WriteBool(false)        // do limited crafting
	// Spawn info, the same block the respawn packet carries.
	s.writeSpawnInfo(&w)
	if s.t.PlayLoginHasOnlineMode {
		w.WriteBool(false) // online mode (26.x)
	}
	w.WriteBool(false) // enforces secure chat
	if err := s.send(s.t.PlayClientboundLogin, w.Bytes()); err != nil {
		return
	}

	// Abilities must match the game mode above, or the client's inventory and
	// flight behave as the other one.
	if err := s.sendAbilities(); err != nil {
		return
	}

	// Required after Login (play): lets the client close the loading screen.
	w.Reset()
	w.WriteByte(13) // game event: start waiting for chunks
	w.WriteFloat(0)
	if err := s.send(s.t.PlayClientboundGameEvent, w.Bytes()); err != nil {
		return
	}

	// Chunks are streamed around wherever the player actually is (see
	// updateChunks), not a fixed grid at spawn.

	// Default spawn. 26.x prepends the dimension name and appends pitch.
	w.Reset()
	if s.t.SetDefaultSpawnHasDimension() {
		w.WriteString(dimensionName)
	}
	w.WriteLong(packPosition(0, 64, 0))
	w.WriteFloat(0) // yaw
	if s.t.SetDefaultSpawnHasPitch() {
		w.WriteFloat(0) // pitch
	}
	if err := s.send(s.t.PlayClientboundSetDefaultSpawnPosition, w.Bytes()); err != nil {
		return
	}

	w.Reset()
	w.WriteVarInt(10)
	if err := s.send(s.t.PlayClientboundSetSimulationDistance, w.Bytes()); err != nil {
		return
	}

	// Synchronize player position: their saved spot, or the spawn point.
	px, py, pz := 0.5, 65.0, 0.5
	s.spawnX, s.spawnY, s.spawnZ = px, py, pz
	yaw, pitch := float32(0), float32(0)
	if hasSaved && saved.HasPos {
		px, py, pz = saved.X, saved.Y, saved.Z
		yaw, pitch = saved.Yaw, saved.Pitch
	}
	s.posX, s.posY, s.posZ = px, py, pz
	s.yaw, s.pitch = yaw, pitch

	w.Reset()
	w.WriteVarInt(1) // teleport id
	w.WriteDouble(px)
	w.WriteDouble(py)
	w.WriteDouble(pz)
	w.WriteDouble(0) // velocity x
	w.WriteDouble(0) // velocity y
	w.WriteDouble(0) // velocity z
	w.WriteFloat(yaw)
	w.WriteFloat(pitch)
	w.WriteInt(0) // relative flags
	if err := s.send(s.t.PlayClientboundPlayerPosition, w.Bytes()); err != nil {
		return
	}

	if err := s.sendHealth(); err != nil {
		return
	}

	// Replay the inventory, or the client shows an empty hotbar on reconnect.
	if hasSaved {
		if err := s.sendInventory(); err != nil {
			return
		}
		if err := s.sendHeldSlot(); err != nil {
			return
		}
	}

	// Stream the chunks around the player's actual position.
	s.updateChunks(px, pz)

	// The entities already in those chunks: mobs wandering and items on the
	// ground get their own Add Entity packets as they move, but a player who
	// joins afterwards would otherwise not see them at all.
	s.srv.sendVisibleEntities(s)

	// The recipe book, so the crafting screen is not empty.
	s.sendRecipeBook()

	s.joinAge = s.srv.Age()

	// The scoreboard: any objectives, their scores, and the sidebar.
	s.srv.sendScoreboard(s)

	// Any boss bars.
	s.srv.sendBossBars(s)

	// In-world presence: the newcomer learns about everyone, and everyone
	// learns about the newcomer. Sent after the teleport so the positions
	// others see are the real ones.
	s.lastX, s.lastY, s.lastZ = px, py, pz
	s.lastYaw, s.lastPitch = yaw, pitch
	s.hasLast = true
	for _, o := range s.srv.sessionList() {
		if o == s {
			continue
		}
		s.sendQuiet(s.t.PlayClientboundAddEntity, addEntityBody(o.entityID, o.uuid, s.t.PlayerEntityType, o.posX, o.posY, o.posZ, o.yaw, o.pitch))
		o.sendQuiet(o.t.PlayClientboundAddEntity, addEntityBody(s.entityID, s.uuid, o.t.PlayerEntityType, s.posX, s.posY, s.posZ, s.yaw, s.pitch))
	}

	log.Printf("player %q joined the world (uuid=%x)", s.name, s.uuid)

	if err := s.sendCommands(); err != nil {
		return
	}
	s.sendWeather()
	s.sendDifficulty()
	// What everyone is holding and how they are posed, both ways.
	s.sendOthersEquipment()
	s.broadcastMainHand()
	s.sendOthersMetadata()
	s.broadcastMetadata()

	// Keep-alive: the client disconnects after ~30 s without one.
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		var id int64
		for {
			select {
			case <-done:
				return
			case <-t.C:
				id++
				var kw mcproto.Writer
				kw.WriteLong(id)
				if err := s.sendQuiet(s.t.PlayClientboundKeepAlive, kw.Bytes()); err != nil {
					return
				}
			}
		}
	}()

	for {
		p, err := s.c.ReadPacket()
		if err != nil {
			log.Printf("client %q disconnected: %v", s.name, err)
			return
		}
		s.handlePlay(p)
	}
}

// handlePlay dispatches one serverbound play packet.
func (s *session) handlePlay(p *mcproto.Packet) {
	r := mcproto.NewReader(p.Data)
	switch p.ID {
	case s.t.PlayServerboundAcceptTeleportation:
		if id, err := r.ReadVarInt(); err == nil {
			log.Printf("<- teleport confirmed (id=%d)", id)
		}
	case s.t.PlayServerboundKeepAlive:
		if id, err := r.ReadLong(); err == nil {
			log.Printf("<- keep-alive ack (id=%d)", id)
		}
	case s.t.PlayServerboundMovePlayerPos, s.t.PlayServerboundMovePlayerPosRot:
		// Fires ~20x/s, so it is tracked rather than logged.
		x, e1 := r.ReadDouble()
		y, e2 := r.ReadDouble()
		z, e3 := r.ReadDouble()
		if e1 == nil && e2 == nil && e3 == nil {
			s.posX, s.posY, s.posZ = x, y, z
			// Cheap when the chunk has not changed; this is what makes the
			// world follow the player instead of ending at spawn.
			s.updateChunks(x, z)
			s.srv.broadcastEntityMove(s)
		}
		if p.ID == s.t.PlayServerboundMovePlayerPosRot {
			if yaw, err := r.ReadFloat(); err == nil {
				s.yaw = yaw
			}
			if pitch, err := r.ReadFloat(); err == nil {
				s.pitch = pitch
			}
		}
		s.readMoveFlags(r)
		s.trackFall()
	case s.t.PlayServerboundMovePlayerRot:
		// Rotation, then the flags.
		if _, err := r.ReadFloat(); err == nil {
			if _, err := r.ReadFloat(); err == nil {
				s.readMoveFlags(r)
			}
		}
	case s.t.PlayServerboundMovePlayerStatusOnly:
		s.readMoveFlags(r)
		s.trackFall()
	case s.t.PlayServerboundChat:
		msg, err := r.ReadString()
		if err != nil {
			return
		}
		log.Printf("<- chat: %s", msg)
		// Broadcast to everyone, sender included, as a player message.
		s.srv.broadcastChat(s.name, msg)
	case s.t.PlayServerboundPlayerAction:
		s.handlePlayerAction(r)
	case s.t.PlayServerboundAttack:
		s.handleAttack(r)
	case s.t.PlayServerboundClientCommand:
		s.handleClientCommand(r)
	case s.t.PlayServerboundUseItemOn:
		s.handleUseItemOn(r)
	case s.t.PlayServerboundUseItem:
		s.handleUseItem(r)
	case s.t.PlayServerboundSetCarriedItem:
		if slot, err := r.ReadUShort(); err == nil && slot < 9 {
			changed := s.heldSlot != int32(slot)
			s.heldSlot = int32(slot)
			// Everyone else sees the item in this player's hand change.
			if changed {
				s.broadcastMainHand()
			}
		}
	case s.t.PlayServerboundSetCreativeModeSlot:
		s.handleCreativeSlot(r)
	case s.t.PlayServerboundSignUpdate:
		s.handleSignUpdate(r)
	case s.t.PlayServerboundContainerClick:
		s.handleContainerClick(r)
	case s.t.PlayServerboundContainerClose:
		s.handleContainerClose(r)
	case s.t.PlayServerboundContainerButtonClick:
		// 0 means this version has no such packet, and the case would then
		// catch whatever id 0 is.
		if s.t.PlayServerboundContainerButtonClick != 0 {
			s.handleContainerButton(r)
		}
	case s.t.PlayServerboundRenameItem:
		s.handleRenameItem(r)
	case s.t.PlayServerboundChatCommand, s.t.PlayServerboundChatCommandSigned:
		s.handleChatCommand(r)
	case s.t.PlayServerboundPlayerInput:
		s.handlePlayerInput(r)
	case s.t.PlayServerboundPickItemFromBlock:
		s.handlePickItemFromBlock(r)
	case s.t.PlayServerboundPlayerCommand:
		s.handlePlayerCommand(r)
	case s.t.PlayServerboundPunch:
		// 26.2 and 1.21.8 have no punch packet, and 0 is
		// accept_teleportation — which an earlier case already takes. The
		// guard keeps that true if the cases are ever reordered.
		if s.t.PlayServerboundPunch != 0 {
			// A punch is a swing of the main hand; the client animates its
			// own arm, so only the other players need telling.
			s.broadcastSwing()
		}
	case s.t.PlayServerboundBlockEntityTagQuery:
		s.handleTagQuery(r)
	case s.t.PlayServerboundChunkBatchReceived:
		// The client reports how many chunks per tick it wants. Read it rather
		// than let it fall through as "unknown": the value is the hook for
		// pacing a faster stream later.
		if rate, err := r.ReadFloat(); err == nil {
			log.Printf("<- chunk rate %.1f/s", rate*20)
		}
	default:
		// Tolerated. Log each distinct ID once: a chatty client (tick end fires
		// ~20x/s) would otherwise flood the trace.
		if s.ignored == nil {
			s.ignored = make(map[int32]bool)
		}
		if !s.ignored[p.ID] {
			s.ignored[p.ID] = true
			log.Printf("<- play 0x%02X len=%d (ignored; logged once per id)", p.ID, len(p.Data))
		}
	}
}

// handleTagQuery answers a client's request for one block entity's data.
//
// The reply is a tag query packet, not block entity data: the transaction id
// is echoed and the tag follows with no position of its own. Vanilla gates this
// on gamemaster permission and answers with a null tag when there is no block
// entity there; this server has no permission model yet, so anyone may ask.
func (s *session) handleTagQuery(r *mcproto.Reader) {
	id, err := r.ReadVarInt()
	if err != nil {
		return
	}
	pos, err := r.ReadLong()
	if err != nil {
		return
	}
	x, y, z := unpackPosition(pos)
	text, ok := s.srv.signs.Get(x, y, z)
	s.sendQuiet(s.t.PlayClientboundTagQuery, tagQueryReply(id, text, ok))
}

// tagQueryReply builds a tag query body: the transaction id, then the block
// entity's data — or a single zero byte, which is how an absent tag is written.
func tagQueryReply(id int32, text world.SignText, ok bool) []byte {
	var w mcproto.Writer
	w.WriteVarInt(id)
	if !ok {
		w.WriteByte(0) // TAG_End: no tag
		return w.Bytes()
	}
	w.WriteBytes(world.SignNBT(text))
	return w.Bytes()
}

// handlePlayerAction handles block breaking. A vanilla client only sends
// Status=Finished for blocks it did not instamine, so breaking is applied on
// Started (creative-like) as well as Finished.
func (s *session) handlePlayerAction(r *mcproto.Reader) {
	status, err := r.ReadVarInt()
	if err != nil {
		return
	}
	pos, err := r.ReadLong()
	if err != nil {
		return
	}
	face, err := r.ReadByte()
	if err != nil {
		return
	}
	seq, err := r.ReadVarInt()
	if err != nil {
		return
	}
	// Every path below acks, so the defer comes before the dispatch.
	defer s.ackBlockChange(seq)
	x, y, z := unpackPosition(pos)
	// ServerboundPlayerActionPacket.Action: 0 starts a dig, 1 changes its
	// direction, 2 ABORTS it, 3 STOPS it — and 3, not 2, is the one that breaks
	// the block.
	//
	// Reading 2 as "finished" meant a block was only ever broken when a player
	// *aborted* a dig, so breaking never worked. The client showed the block gone
	// from its own prediction while the server still had it: that is the "the
	// blocks I removed came back" report. Once the ack for an aborted dig started
	// being sent, the same bug became "it will not let me remove stuff", because
	// the client now drops the prediction immediately.
	if status == 0 {
		// The dig begins: the server tracks it from here, so the cracking
		// texture advances for everyone else instead of appearing once and
		// being wiped.
		s.startDig(x, y, z, int32(face))
		return
	}
	if status == 1 {
		// A different face: the dig carries on, and the cracking turns with it.
		s.digDir = int32(face)
		return
	}
	if status == 2 {
		// Aborted: clear the cracking, and break nothing.
		s.endDig()
		return
	}
	if status != 3 {
		// A drop, a swap: not a break.
		return
	}
	// What is about to go decides the sound and the particles, so read it
	// first.
	broken := s.world.Block(x, y, z)
	brokenName, _, _ := s.srv.blockStateName(s.t, broken)

	// A bell is rung, not broken: vanilla's BellBlock answers a hit with a
	// block event carrying the face that was struck, and the client swings the
	// bell from it. Breaking it here meant a bell could not be rung at all.
	if brokenName == "minecraft:bell" {
		s.srv.blockEvent(s.t, x, y, z, brokenName, 1, int32(face))
		s.srv.playBlockSound(s.t, x, y, z, brokenName, "hit")
		s.endDig()
		return
	}

	// A block the game says can never be broken stays, whatever the client
	// asked for: bedrock, a barrier, the end portal frame.
	if unbreakable(s.t, broken) {
		// Nothing will ever break it, so the dig is over: leaving it running
		// would keep the cracking sound going for as long as the player kept
		// the button down.
		s.endDig()
		return
	}

	s.setBlock(x, y, z, s.t.BlockAir, seq)
	// A broken block takes its block entity with it. A sign's text and a
	// banner's layers live in this server's own stores rather than in the world
	// file, so leaving them behind means a sign placed there later inherits the
	// old text — the block's contents coming back after the block is gone.
	s.srv.signs.Delete(x, y, z)
	s.world.Banners().Delete(x, y, z)
	// Breaking a container drops what was in it.
	s.srv.containers.Remove(x, y, z)
	// The client shows the block's break particles and plays its sound; without
	// them a block vanishes in silence.
	s.srv.levelEvent(s.t, x, y, z, levelEventBreak, broken)
	s.srv.playBlockSound(s.t, x, y, z, brokenName, "break")
	if brokenName != "" {
		s.srv.countStat(s.uuid, statMined, brokenName)
	}
	// Whoever else was watching gets the cracking texture cleared, and the dig
	// is over.
	s.digActive = false
	s.digStage = -1
	s.srv.destroyStage(s.t, s.entityID, x, y, z, -1)
	// Breaking drops the block in survival; in creative it leaves nothing,
	// which is what the mode is for. A block that needs the right tool drops
	// only for that tool: mining stone with a bare fist gives nothing, which is
	// what vanilla does.
	if drops, _ := s.srv.rule("doTileDrops"); s.gameMode == gameModeSurvival && drops && s.canHarvest(broken) {
		s.srv.dropBrokenBlock(s.t, x, y, z, brokenName)
	}
}

// handleUseItem is a right click with nothing under it. A throwable leaves the
// hand — this is the packet the client sends for that, separate from the one it
// sends when a block is in the way — and nothing else uses the item yet.
func (s *session) handleUseItem(r *mcproto.Reader) {
	if _, err := r.ReadVarInt(); err != nil { // which hand
		return
	}
	// A sequence number follows, which the client uses to match the action to
	// the inventory state it sent; this server does not predict, so it is read
	// and dropped.
	if _, err := r.ReadVarInt(); err != nil {
		return
	}
	held := s.mainHandItem()
	if held.Empty() {
		return
	}
	s.throwItem(held.ID)
}

// handleUseItemOn handles block placement against the face that was clicked.
func (s *session) handleUseItemOn(r *mcproto.Reader) {
	if _, err := r.ReadVarInt(); err != nil { // hand
		return
	}
	pos, err := r.ReadLong()
	if err != nil {
		return
	}
	face, err := r.ReadVarInt()
	if err != nil {
		return
	}
	// The cursor is where on the block face the player clicked, as a fraction
	// of the block: it decides top/bottom halves and slab types.
	var cursorX, cursorY, cursorZ float32
	if cursorX, err = r.ReadFloat(); err != nil {
		return
	}
	if cursorY, err = r.ReadFloat(); err != nil {
		return
	}
	if cursorZ, err = r.ReadFloat(); err != nil {
		return
	}
	for range 2 { // inside block, world border hit
		if _, err := r.ReadByte(); err != nil {
			return
		}
	}
	seq, err := r.ReadVarInt()
	if err != nil {
		return
	}
	x, y, z := unpackPosition(pos)

	// A use-item-on is a block prediction too, and the paths below that do not
	// place a block (opening a container, a lectern, a door, an empty hand) used
	// to leave the sequence un-acked — the client then kept its predicted block
	// in the world for good. Deferred so it lands after any block update.
	defer s.ackBlockChange(seq)

	// A lectern takes a book, or shows the one it holds. It is in the container
	// table, so this has to come first: the generic path would open the screen
	// without the page property.
	if s.useLectern(x, y, z) {
		return
	}

	// Using a container block opens it rather than placing against it.
	if _, ok := s.t.ContainerMenus[s.world.Block(x, y, z)]; ok {
		if s.openContainer(x, y, z) {
			return
		}
	}

	// A door, trapdoor or fence gate opens instead of being built against, and
	// it does so whatever is in hand — including an empty hand. It is the block
	// that was CLICKED (x, y, z): bx/by/bz is the neighbour the face points at,
	// which is where a block would go.
	if s.toggleOpenable(x, y, z) {
		return
	}

	item := s.heldItemID()
	if item == 0 {
		return
	}

	dx, dy, dz := faceOffset(face)
	bx, by, bz := x+dx, y+dy, z+dz

	// A spawn egg creates the mob it holds. Creative mode does not consume it.
	if name, ok := s.t.ItemName[item]; ok {
		if s.useEndPortalFrame(name, x, y, z, item, seq) {
			return
		}
		if mob, isEgg := spawnEggEntity(name); isEgg {
			// The name keeps its namespace: minecraft:zombie_spawn_egg holds
			// minecraft:zombie, not a bare "zombie".
			if typ, ok := s.t.EntityIDs[mob]; ok {
				s.srv.spawnEntity(kindMob, typ, float64(bx)+0.5, float64(by), float64(bz)+0.5)
			} else {
				log.Printf("spawn egg %q holds unknown mob %q", name, mob)
			}
			return
		}
		// Flint and steel lights TNT, or drops fire on the block face it was
		// used on. Fire does not spread or go out yet: that needs block ticks.
		if name == "minecraft:flint_and_steel" {
			if s.blockName(x, y, z) == "minecraft:tnt" {
				s.setBlockUpdate(x, y, z, s.t.BlockAir)
				s.srv.spawnEntity(kindTNT, s.t.EntityIDs["minecraft:tnt"],
					float64(x)+0.5, float64(y), float64(z)+0.5).Fuse = tntFuse
				return
			}
			if s.world.Block(bx, by, bz) == s.t.BlockAir {
				if fire, ok := s.srv.blockState(s.t, "minecraft:fire", nil); ok {
					s.setBlockUpdate(bx, by, bz, fire)
				}
			}
			return
		}
	}

	// A bucket of water pours a source where the player clicked, and is left
	// empty in the hand.
	if item == s.t.ItemIDs["minecraft:water_bucket"] {
		s.pourWater(bx, by, bz)
		s.consumeHeldItem(item, s.t.ItemIDs["minecraft:bucket"])
		return
	}
	if item == s.t.ItemIDs["minecraft:lava_bucket"] {
		s.pourLava(bx, by, bz)
		s.consumeHeldItem(item, s.t.ItemIDs["minecraft:bucket"])
		return
	}

	// Nothing in hand that places a block: using it does nothing. (The old
	// fallback placed dirt for an empty hand.)
	if _, ok := s.t.ItemBlockName[item]; !ok {
		return
	}
	spec := s.placeSpec(item, face, cursorX, cursorY, cursorZ, bx, by, bz)
	s.setBlock(bx, by, bz, spec.state, seq)

	// The sound of the block going down, for everyone nearby.
	if name, _, ok := s.srv.blockStateName(s.t, spec.state); ok {
		s.srv.playBlockSound(s.t, bx, by, bz, name, "place")
	}

	// A chest placed beside another pairs them: both states change.
	s.pairChest(spec, bx, by, bz)

	// A bed and a door are two blocks: vanilla places the other half with the
	// first, and half a bed is what you see without it.
	if other, ox, oy, oz, ok := s.otherHalf(spec, bx, by, bz); ok {
		s.setBlockUpdate(ox, oy, oz, other)
	}

	// Signs are block entities: the client only offers its editor when the
	// server asks, and the block must already exist (the Block Update sent by
	// setBlock above provides that).
	if s.t.SignBlockStates[spec.state] {
		s.openSignEditor(bx, by, bz)
	}

	// A banner's pattern layers are not in its block state: they arrive as the
	// banner_patterns component of the item that was placed. Record them for the
	// block entity, or every banner renders plain.
	if name, _, ok := s.srv.blockStateName(s.t, spec.state); ok && strings.HasSuffix(name, "_banner") {
		s.rememberBannerPatterns(bx, by, bz)
	}
}

// rememberBannerPatterns copies the banner_patterns component of the held item
// to the banner at (x, y, z). The value is BannerPatternLayers, which is the
// block entity's patterns field as well, so it is stored as it came rather than
// decoded and re-encoded.
func (s *session) rememberBannerPatterns(x, y, z int32) {
	if s.heldSlot < 0 || s.heldSlot > 8 {
		return
	}
	it := s.inv[27+s.heldSlot]
	for _, c := range it.Components {
		if c.Removed || c.Type != s.t.BannerPatternsComponent {
			continue
		}
		s.world.Banners().Put(x, y, z, c.Value)
		return
	}
	// No component: a plain banner. Whoever placed a patterned one here before
	// is gone, so the layers go with it.
	s.world.Banners().Delete(x, y, z)
}

// updateChunks streams the chunks around (x, z) and forgets any that fall out
// of range, so client memory stays bounded as the player travels.
func (s *session) updateChunks(x, z float64) {
	cx, cz := int32(math.Floor(x/16)), int32(math.Floor(z/16))
	if s.haveChunks && cx == s.curCX && cz == s.curCZ {
		return
	}
	s.curCX, s.curCZ = cx, cz
	s.haveChunks = true
	if s.sent == nil {
		s.sent = make(map[[2]int32]bool)
	}

	var w mcproto.Writer
	w.WriteVarInt(cx)
	w.WriteVarInt(cz)
	s.sendQuiet(s.t.PlayClientboundSetChunkCacheCenter, w.Bytes())

	want := make(map[[2]int32]bool, (2*viewRadius+1)*(2*viewRadius+1))
	// Chunk batching: the client uses these markers to know when a burst is
	// complete — it holds the loading screen until the batch finishes, so
	// without them a fast flight can look like an empty world.
	s.sendQuiet(s.t.PlayClientboundChunkBatchStart, nil)
	var batch int32
	for dz := int32(-viewRadius); dz <= viewRadius; dz++ {
		for dx := int32(-viewRadius); dx <= viewRadius; dx++ {
			k := [2]int32{cx + dx, cz + dz}
			want[k] = true
			if s.sent[k] {
				continue
			}
			body := s.world.Payload(k[0], k[1])
			// A chunk carrying player work is logged with a hash of its bytes,
			// so a resend that brings a removed block back can be told from one
			// that carries the current world.
			//
			// This fires only where a chunk is actually *sent*: the first
			// stream of it, or after it was forgotten and comes back into
			// range. Nothing here re-sends a chunk because a block changed —
			// that is a block update — so silence in this log is informative:
			// it says the chunk was not re-sent, which rules a chunk payload
			// out as the source of a ghost inside a session.
			if edits := s.world.EditsInChunk(k[0], k[1]); edits > 0 {
				log.Printf("chunk (%d,%d) sent to %q: %d bytes, sum %08x, %d edits",
					k[0], k[1], s.name, len(body), fnv32(body), edits)
			}
			s.sendQuiet(s.t.PlayClientboundLevelChunkWithLight, body)
			s.sent[k] = true
			batch++
		}
	}
	if batch > 0 {
		var f mcproto.Writer
		f.WriteVarInt(batch)
		s.sendQuiet(s.t.PlayClientboundChunkBatchFinished, f.Bytes())
	}
	for k := range s.sent {
		if want[k] {
			continue
		}
		var f mcproto.Writer
		f.WriteInt(k[1]) // chunk Z
		f.WriteInt(k[0]) // chunk X
		s.sendQuiet(s.t.PlayClientboundForgetLevelChunk, f.Bytes())
		delete(s.sent, k)
	}
}

// openSignEditor asks the client to open the sign UI at a position.
func (s *session) openSignEditor(x, y, z int32) {
	var w mcproto.Writer
	w.WriteLong(packPosition(int(x), int(y), int(z)))
	w.WriteVarInt(1) // text slot: 0 = back, 1 = front
	if err := s.send(s.t.PlayClientboundOpenSignEditor, w.Bytes()); err != nil {
		return
	}
	log.Printf("open sign editor at (%d,%d,%d)", x, y, z)
}

// handleSignUpdate stores the text the client sent and replays it to everyone,
// which is what makes the text visible on the sign.
func (s *session) handleSignUpdate(r *mcproto.Reader) {
	pos, err := r.ReadLong()
	if err != nil {
		return
	}
	var lines [4]string
	for i := range 4 {
		line, err := r.ReadString()
		if err != nil {
			return
		}
		lines[i] = line
	}
	slot, err := r.ReadVarInt()
	if err != nil {
		return
	}
	x, y, z := unpackPosition(pos)

	text, _ := s.srv.signs.Get(x, y, z)
	if slot == 1 {
		text.Front = lines
	} else {
		text.Back = lines
	}
	s.srv.signs.Put(x, y, z, text)
	s.srv.broadcastSign(x, y, z, text)
	log.Printf("sign (%d,%d,%d) slot %d <- %q", x, y, z, slot, lines)
}

// heldBlock returns the block state the player is holding, falling back to dirt
// when the item is empty or places nothing.
func (s *session) heldBlock() int32 {
	if id := s.heldItemID(); id != 0 {
		if state, ok := s.t.ItemBlockState[id]; ok {
			return state
		}
	}
	return s.t.BlockDirt
}

// heldItemID is the item in the selected hotbar slot, or 0 when the hand is
// empty. The hotbar sits at the end of the inventory array.
func (s *session) heldItemID() int32 {
	if s.heldSlot < 0 || s.heldSlot > 8 {
		return 0
	}
	it := s.inv[27+s.heldSlot]
	if it.Empty() {
		return 0
	}
	return it.ID
}

// handleCreativeSlot records what a creative-mode client put in a hotbar slot.
// Only the item ID is read: the data components that follow are not needed to
// know which block the item places.
func (s *session) handleCreativeSlot(r *mcproto.Reader) {
	slot, err := r.ReadUShort()
	if err != nil {
		return
	}
	// The Slot structure leads with the item COUNT; the item ID only follows
	// when the count is non-zero. Reading the count as the ID made every
	// non-empty slot look like item 1 (stone).
	count, err := r.ReadVarInt()
	if err != nil {
		return
	}
	item := int32(0)
	var comps []world.Component
	if count > 0 {
		if item, err = r.ReadVarInt(); err != nil {
			return
		}
		// The components follow. They carry everything the item is beyond its
		// id and count — a custom name, a book's pages, banner patterns — so
		// they are kept as they came and sent back unchanged.
		if comps, err = creativeComponents(r); err != nil {
			return
		}
	}
	// Window slots 9-44 are the player's main inventory and hotbar: keeping the
	// whole range (not just the hotbar) is what lets a container window's
	// player side be sent verbatim.
	if slot >= 9 && slot <= 44 {
		idx := slot - 9
		if count <= 0 || item == 0 {
			s.inv[idx] = world.Item{}
		} else {
			s.inv[idx] = world.Item{ID: item, Count: count, Components: comps}
		}
		// Changing the held slot changes what other players see in the hand.
		if int32(idx) == 27+s.heldSlot {
			s.broadcastMainHand()
		}
	}
}

// saveState persists position, hotbar and selection so a reconnect resumes
// where the player left off.
func (s *session) saveState() {
	if s.uuidKey == "" {
		return
	}
	s.srv.players.Put(s.uuidKey, world.PlayerState{
		Name:     s.name,
		X:        s.posX,
		Y:        s.posY,
		Z:        s.posZ,
		Yaw:      s.yaw,
		Pitch:    s.pitch,
		Inv:      s.inv,
		HeldSlot: s.heldSlot,
		HasPos:   true,
	})
}

// sendInventory replays the hotbar into window 0 (the player inventory).
func (s *session) sendInventory() error {
	const slots = 46 // crafting 0-8, main 9-35, hotbar 36-44, offhand 45
	var w mcproto.Writer
	w.WriteVarInt(0) // window id: player inventory
	w.WriteVarInt(0) // state id
	w.WriteVarInt(slots)
	for i := range slots {
		if i >= 9 && i <= 44 {
			s.writeSlot(&w, s.inv[i-9])
			continue
		}
		s.writeSlot(&w, world.Item{})
	}
	s.writeSlot(&w, s.carried) // carried item
	return s.send(s.t.PlayClientboundContainerSetContent, w.Bytes())
}

// writeSlot emits one Slot: the item count leads, and the item ID and component
// sets follow only when the count is non-zero.
func (s *session) writeSlot(w *mcproto.Writer, it world.Item) {
	if it.Empty() {
		w.WriteVarInt(0)
		return
	}
	w.WriteVarInt(it.Count)
	w.WriteVarInt(it.ID)
	// The component patch: how many components are set, how many are taken
	// away, then the values. Every component is passed through untouched, which
	// is what keeps a written book or a banner pattern intact without an
	// encoder for each component type.
	var added, removed int32
	for _, c := range it.Components {
		if c.Removed {
			removed++
		} else {
			added++
		}
	}
	w.WriteVarInt(added)
	w.WriteVarInt(removed)
	for _, c := range it.Components {
		if c.Removed {
			continue
		}
		w.WriteVarInt(c.Type)
		w.WriteBytes(c.Value)
	}
	for _, c := range it.Components {
		if c.Removed {
			w.WriteVarInt(c.Type)
		}
	}
}

// itemName is the item's custom name, or "" when it has none.
func itemName(t proto.Tables, it world.Item) string {
	value, ok := it.Component(t.CustomNameComponent)
	if !ok {
		return ""
	}
	return textComponentName(value)
}

// setItemName sets or clears an item's custom name.
func setItemName(t proto.Tables, it *world.Item, name string) {
	if name == "" {
		it.RemoveComponent(t.CustomNameComponent)
		return
	}
	it.SetComponent(t.CustomNameComponent, nbtText(name))
}

// nbtTranslate encodes a network-NBT translatable text component:
// {"translate": s}. The client renders it in its own language, which is why a
// menu title is a key like "container.chest" rather than the word "Chest".
func nbtTranslate(s string) []byte {
	var w mcproto.Writer
	w.WriteByte(0x0A) // TAG_Compound
	w.WriteByte(0x08) // TAG_String
	w.WriteUShort(uint16(len("translate")))
	w.WriteBytes([]byte("translate"))
	w.WriteUShort(uint16(len(s)))
	w.WriteBytes([]byte(s))
	w.WriteByte(0x00) // TAG_End
	return w.Bytes()
}

func packPosition(x, y, z int) int64 {
	return int64((x&0x3FFFFFF)<<38 | (z&0x3FFFFFF)<<12 | (y & 0xFFF))
}

// skipHashedSlot reads a Slot in its hashed form: what the client sends in a
// container click. The component *hashes* are not needed to move stacks
// around, so they are read and dropped.
func skipHashedSlot(r *mcproto.Reader) (world.Item, error) {
	has, err := r.ReadByte()
	if err != nil {
		return world.Item{}, err
	}
	if has == 0 {
		return world.Item{}, nil
	}
	id, err := r.ReadVarInt()
	if err != nil {
		return world.Item{}, err
	}
	count, err := r.ReadVarInt()
	if err != nil {
		return world.Item{}, err
	}
	for range 2 { // added component hashes, then removed component types
		n, err := r.ReadVarInt()
		if err != nil {
			return world.Item{}, err
		}
		for range n {
			if _, err := r.ReadVarInt(); err != nil {
				return world.Item{}, err
			}
			if _, err := r.ReadInt(); err != nil { // CRC32C of the component
				return world.Item{}, err
			}
		}
	}
	return world.Item{ID: id, Count: count}, nil
}

// containerWindowID is the window id used for container screens. 0 is the
// player's own inventory, which is always "open" from the server's side.
const containerWindowID = 1

// openContainer shows the menu that the block at a position opens.
//
// Which menu, how many slots it has and what it is called all come from the
// generated table, keyed by the block state: a barrel and a chest share
// generic_9x3 but not their title, and a hopper opens a five-slot menu.
func (s *session) openContainer(x, y, z int32) bool {
	s.winMu.Lock()
	defer s.winMu.Unlock()
	spec, ok := s.t.ContainerMenus[s.world.Block(x, y, z)]
	if !ok {
		return false
	}
	size := int(spec.Slots)
	items := s.srv.containers.Get(x, y, z, size)
	s.containerOpen = true
	// A chest creaks when it opens; a furnace says nothing. The sound is sent
	// to everyone near it, the player opening it included.
	s.containerSound(s.t, x, y, z, "open")
	s.containerPos = [3]int32{x, y, z}
	s.containerState = 0
	s.containerSize = spec.Slots
	s.containerKind = spec.ID
	s.containerTitle = spec.Title
	s.carried = world.Item{}
	if isFurnaceTitle(spec.Title) { // an inventory that cooks is ticked
		s.srv.containers.MarkFurnace(x, y, z)
	}

	var title mcproto.Writer
	title.WriteBytes(nbtTranslate(spec.Title))
	var w mcproto.Writer
	w.WriteVarInt(containerWindowID)
	w.WriteVarInt(spec.ID)
	w.WriteBytes(title.Bytes())
	if err := s.send(s.t.PlayClientboundOpenScreen, w.Bytes()); err != nil {
		return false
	}
	if isAnvilTitle(spec.Title) {
		s.anvilName = ""
		s.anvilCost = 0
		s.refreshAnvilResult(items)
	}
	s.refreshCraftingResult(items)
	s.sendContainerContentLocked(items)
	if isFurnaceTitle(spec.Title) {
		s.sendContainerData(x, y, z)
	}
	if isAnvilTitle(spec.Title) {
		s.sendAnvilCost(s.anvilCost)
	}
	// The lid: a chest, ender chest or shulker animates from a block event, a
	// barrel from its OPEN state. The opening sound is separate and was sent
	// above, the way vanilla keeps the two apart.
	s.srv.containerOpened(s.t, s.world, x, y, z, true)
	log.Printf("open container at (%d,%d,%d) menu %d (%d slots)", x, y, z, spec.ID, spec.Slots)
	return true
}

// sendContainerContent resends the whole window: the container's slots, the
// player's 36, and whatever is on the cursor. Resending everything is simpler
// than tracking which slots changed, and the window is small.
func (s *session) sendContainerContent(items []world.Item) {
	s.winMu.Lock()
	defer s.winMu.Unlock()
	s.sendContainerContentLocked(items)
}

func (s *session) sendContainerContentLocked(items []world.Item) {
	size := int(s.containerSize)
	var w mcproto.Writer
	w.WriteVarInt(containerWindowID)
	w.WriteVarInt(s.containerState)
	w.WriteVarInt(int32(size + 36))
	for i := range size {
		s.writeSlot(&w, items[i])
	}
	for i := range 36 {
		s.writeSlot(&w, s.inv[i])
	}
	s.writeSlot(&w, s.carried)
	if err := s.send(s.t.PlayClientboundContainerSetContent, w.Bytes()); err != nil {
		return
	}
}

// applyClick applies one container click to the window's slots and returns the
// carried stack. The modes follow vanilla's ContainerInput: 0 pickup, 1 quick
// move (shift-click), 2 swap (hotkey), 3 clone (creative), 6 pickup-all.
//
// Throw (4) and quick-craft/drag (5) are deliberately not implemented: a wrong
// guess would corrupt a stack, and leaving them untouched only means the client
// has to reopen the window. -999 means the click was outside the window; the
// stack stays carried rather than being destroyed.
func applyClick(w []world.Item, carried world.Item, slot, mode, button, containerSize int32) world.Item {
	if slot < 0 || slot >= int32(len(w)) {
		return carried
	}
	cur := w[slot]

	switch mode {
	case 0: // pickup: the plain click
		switch {
		case carried.Empty() && !cur.Empty():
			w[slot] = world.Item{}
			return cur
		case !carried.Empty() && cur.Empty():
			w[slot] = carried
			return world.Item{}
		case !carried.Empty() && cur.Same(carried):
			space := 64 - cur.Count
			if space > carried.Count {
				space = carried.Count
			}
			cur.Count += space
			carried.Count -= space
			w[slot] = cur
			if carried.Count <= 0 {
				return world.Item{}
			}
			return carried
		case !carried.Empty():
			w[slot] = carried
			return cur
		}

	case 1: // quick move: shift-click sends the stack to the other half
		if slot < containerSize {
			moveInto(w, slot, playerSlots(containerSize))
		} else {
			moveInto(w, slot, containerSlots(containerSize))
		}

	case 2: // swap: a hotbar key, or 40 for the offhand, which we do not model
		if button >= 0 && button <= 8 {
			hotbar := containerSize + button
			w[slot], w[hotbar] = w[hotbar], cur
		}

	case 3: // clone: creative middle-click copies a full stack
		if !cur.Empty() {
			cur.Count = 64
			return cur
		}

	case 6: // pickup-all: double-click gathers matching stacks onto the cursor
		if carried.Empty() {
			return carried
		}
		for i, s := range w {
			if int32(i) == slot || s.Empty() || !s.Same(carried) || s.Count == 64 {
				continue
			}
			space := 64 - carried.Count
			if space <= 0 {
				break
			}
			n := min(space, s.Count)
			carried.Count += n
			s.Count -= n
			w[i] = s
		}
	}
	return carried
}

// moveInto moves the stack at from into the listed slots: matching stacks are
// topped up first, then an empty slot takes the rest.
func moveInto(w []world.Item, from int32, dst []int32) {
	moving := w[from]
	if moving.Empty() {
		return
	}
	for _, d := range dst {
		if d == from {
			continue
		}
		s := w[d]
		if s.Empty() || !s.Same(moving) || s.Count >= 64 {
			continue
		}
		n := min(64-s.Count, moving.Count)
		s.Count += n
		moving.Count -= n
		w[d] = s
		if moving.Count == 0 {
			w[from] = world.Item{}
			return
		}
	}
	for _, d := range dst {
		if d == from {
			continue
		}
		if w[d].Empty() {
			w[d] = moving
			w[from] = world.Item{}
			return
		}
	}
	w[from] = moving
}

// playerSlots and containerSlots list the window's two halves. The player's
// half always starts after the container's, whatever size the menu is.
func playerSlots(containerSize int32) []int32 {
	out := make([]int32, 36)
	for i := range out {
		out[i] = containerSize + int32(i)
	}
	return out
}

func containerSlots(containerSize int32) []int32 {
	out := make([]int32, containerSize)
	for i := range out {
		out[i] = int32(i)
	}
	return out
}

// window assembles the slot array the client sees: the container's slots then
// the player's, so a click mode is one operation over a single slice.
func (s *session) window(items []world.Item) []world.Item {
	size := int(s.containerSize)
	w := make([]world.Item, 0, size+36)
	w = append(w, items[:size]...)
	w = append(w, s.inv[:]...)
	return w
}

// scatterWindow writes an assembled window back into its two parts.
func (s *session) scatterWindow(items, w []world.Item) {
	size := int(s.containerSize)
	copy(items[:size], w[:size])
	copy(s.inv[:], w[size:])
}

func (s *session) handleContainerClick(r *mcproto.Reader) {
	window, err := r.ReadVarInt()
	if err != nil {
		return
	}
	if _, err := r.ReadVarInt(); err != nil { // state id
		return
	}
	rawSlot, err := r.ReadUShort()
	if err != nil {
		return
	}
	slot := int32(int16(rawSlot)) // -999 means "outside the window"
	button, err := r.ReadByte()
	if err != nil {
		return
	}
	mode, err := r.ReadVarInt()
	if err != nil {
		return
	}
	n, err := r.ReadVarInt()
	if err != nil {
		return
	}
	for range n { // slots the client believes changed
		if _, err := r.ReadUShort(); err != nil {
			return
		}
		if _, err := skipHashedSlot(r); err != nil {
			return
		}
	}
	if _, err := skipHashedSlot(r); err != nil {
		return
	}

	s.winMu.Lock()
	defer s.winMu.Unlock()
	if !s.containerOpen || window != containerWindowID {
		return
	}

	x, y, z := s.containerPos[0], s.containerPos[1], s.containerPos[2]
	items := s.srv.containers.Get(x, y, z, int(s.containerSize))
	// A crafting table shows a preview in slot 0, so it is recomputed before
	// the click is interpreted rather than being stored server-side.
	s.refreshCraftingResult(items)
	s.refreshAnvilResult(items)
	w := s.window(items)

	switch {
	case slot == craftResultSlot && s.isCraftingMenu():
		s.craftResultClick(items, w, mode, &s.carried)
	case slot == anvilResultSlot && isAnvilTitle(s.containerTitle):
		s.anvilClick(items, w, mode, &s.carried)
	default:
		// The cursor stack is the server's own: the client's copy carries only
		// component hashes, so trusting it would drop a custom name.
		s.carried = applyClick(w, s.carried, slot, mode, int32(button), s.containerSize)
	}
	s.scatterWindow(items, w)
	// Slots that hold a computed result rather than a stored stack must not
	// keep anything a shift-click pushed into them.
	s.reclaimResultSlot(items, w)
	s.refreshCraftingResult(items)
	s.refreshAnvilResult(items)
	if isAnvilTitle(s.containerTitle) {
		s.sendAnvilCost(s.anvilCost)
	}

	s.srv.containers.Put(x, y, z, items)
	s.containerState++
	s.sendContainerContentLocked(items)
}

// handleContainerClose clears the open window; the client already closed its UI.
func (s *session) handleContainerClose(r *mcproto.Reader) {
	if _, err := r.ReadVarInt(); err != nil {
		return
	}
	s.winMu.Lock()
	defer s.winMu.Unlock()
	if !s.containerOpen {
		return
	}
	if s.isCraftingMenu() {
		x, y, z := s.containerPos[0], s.containerPos[1], s.containerPos[2]
		items := s.srv.containers.Get(x, y, z, int(s.containerSize))
		s.srv.containers.Put(x, y, z, s.closeCraftingGrid(items))
		if err := s.sendInventory(); err != nil {
			return
		}
	}
	// Before the position is cleared: the container being closed is the one
	// that was open.
	if s.containerOpen {
		s.containerSound(s.t, s.containerPos[0], s.containerPos[1], s.containerPos[2], "close")
		s.srv.containerOpened(s.t, s.world, s.containerPos[0], s.containerPos[1], s.containerPos[2], false)
	}
	s.containerOpen = false
	s.carried = world.Item{}
	log.Printf("container closed")
}

// sendHeldSlot restores the selected hotbar slot.
func (s *session) sendHeldSlot() error {
	if s.heldSlot < 0 || s.heldSlot > 8 {
		return nil
	}
	var w mcproto.Writer
	w.WriteVarInt(s.heldSlot)
	return s.send(s.t.PlayClientboundSetHeldSlot, w.Bytes())
}

// ackBlockChange tells the client that its prediction for a block action has
// been dealt with.
//
// Every block action carries a sequence, and vanilla acknowledges each one. A
// sequence left un-acked leaves the client holding its prediction for good — a
// ghost block: the client shows one state and the server holds another, and the
// disagreement shows up whenever that chunk is sent again, which looks like a
// block that was removed coming back. That is why every path through the two
// block-action handlers acks, including the ones that do nothing to the world.
func (s *session) ackBlockChange(seq int32) {
	var w mcproto.Writer
	w.WriteVarInt(seq)
	_ = s.sendQuiet(s.t.PlayClientboundBlockChangedAck, w.Bytes())
}

// setBlock records a change and pushes it out. The ack for the action is the
// caller's, sent when the handler finishes, so that it lands after this update:
// an ack before it makes the client roll back to the old block for a frame.
func (s *session) setBlock(x, y, z, id, seq int32) {
	s.setBlockUpdate(x, y, z, id)
}

// setBlockUpdate records a block change and tells everyone who can see it,
// without acking a prediction: the change may not have been the actor's (the
// second half of a door was never predicted, and nobody else predicted
// anything at all).
//
// Vanilla broadcasts a block change to every tracking player. Sending it only
// to the actor leaves everyone else holding a ghost block until the chunk is
// resent — and their client also derives the break particles and sound from
// the update, which is why no separate effect packet exists for a player's
// break.
func (s *session) setBlockUpdate(x, y, z, id int32) {
	cx, cz, secs := s.world.SetBlock(x, y, z, id)
	// Every player-driven change is logged: when a block that was removed turns
	// up again, the sequence of changes and the chunk sends around it is what
	// says which one carried the stale state.
	log.Printf("block change (%d,%d,%d) -> %d by %q", x, y, z, id, s.name)
	// Water next to a change may have to flow again.
	s.srv.enqueueFluids(x, y, z)
	// And lava next to it may have to set: a bucket poured beside the other
	// fluid reacts at once, the way LiquidBlock.neighborChanged does.
	s.srv.fluidNeighbourChanged(s.t, s.world, x, y, z)
	// A block that emits light lights its surroundings; one that blocked light
	// may have been shadowing them.
	s.world.RelightBlock(x, y, z)

	var u mcproto.Writer
	u.WriteLong(packPosition(int(x), int(y), int(z)))
	u.WriteVarInt(id)
	update := u.Bytes()
	var light []byte
	if len(secs) > 0 {
		light = s.world.LightUpdate(cx, cz, secs)
	}

	if err := s.send(s.t.PlayClientboundBlockUpdate, update); err != nil {
		return
	}
	if light != nil {
		if err := s.send(s.t.PlayClientboundLightUpdate, light); err != nil {
			return
		}
	}

	// Block light spreads up to fifteen blocks, so the sections that changed
	// are not only this one.
	lit := s.world.Light().SectionsNear(x, y, z, world.MaxLight-1, s.world.MinY())
	if len(lit) > 0 {
		log.Printf("block light: %d chunk(s) around (%d,%d,%d)", len(lit), x, y, z)
	}
	for chunk, sections := range lit {
		body := s.world.LightUpdate(chunk[0], chunk[1], sections)
		for _, o := range s.srv.sessionList() {
			if o.t.Protocol != s.t.Protocol || !o.sees(x, y, z) {
				continue
			}
			_ = o.sendQuiet(o.t.PlayClientboundLightUpdate, body)
		}
	}

	sent := 0
	for _, o := range s.srv.sessionList() {
		// Block state ids are per protocol: a session on another version is
		// reading its own world, so it must not be sent this one's ids.
		if o == s || o.t.Protocol != s.t.Protocol || !o.sees(x, y, z) {
			continue
		}
		if err := o.sendQuiet(o.t.PlayClientboundBlockUpdate, update); err != nil {
			continue
		}
		if light != nil {
			_ = o.sendQuiet(o.t.PlayClientboundLightUpdate, light)
		}
		sent++
	}
	log.Printf("block (%d,%d,%d) -> %d (%d light sections, %d other players)", x, y, z, id, len(secs), sent)
}

// blockViewRadius is how far a block change is broadcast, in blocks. Vanilla
// sends block updates to whichever players are tracking that chunk; a distance
// test is a cheap stand-in that never misses a player who could see it.
const blockViewRadius = 64

// sees reports whether a block position is close enough to a player to bother
// sending it to them.
func (s *session) sees(x, y, z int32) bool {
	return s.seesPos(float64(x), float64(y), float64(z))
}

// seesPos is the same test for a point that is already in world coordinates,
// such as another player's position.
func (s *session) seesPos(x, y, z float64) bool {
	dx, dy, dz := s.posX-x, s.posY-y, s.posZ-z
	return dx*dx+dy*dy+dz*dz < blockViewRadius*blockViewRadius
}

func nbtText(s string) []byte {
	var w mcproto.Writer
	w.WriteByte(0x0A) // TAG_Compound
	w.WriteByte(0x08) // TAG_String
	w.WriteUShort(uint16(len("text")))
	w.WriteBytes([]byte("text"))
	w.WriteUShort(uint16(len(s)))
	w.WriteBytes([]byte(s))
	w.WriteByte(0x00) // TAG_End
	return w.Bytes()
}

// faceOffset maps a face enum to its unit offset.
func faceOffset(face int32) (int32, int32, int32) {
	switch face {
	case 0:
		return 0, -1, 0 // bottom
	case 1:
		return 0, 1, 0 // top
	case 2:
		return 0, 0, -1 // north
	case 3:
		return 0, 0, 1 // south
	case 4:
		return -1, 0, 0 // west
	default:
		return 1, 0, 0 // east
	}
}

func unpackPosition(v int64) (int32, int32, int32) {
	x := int32(v >> 38)
	y := int32(v << 52 >> 52)
	z := int32(v << 26 >> 38)
	return x, y, z
}
