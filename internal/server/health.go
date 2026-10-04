package server

import (
	"log"
	"math"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Health, damage, death and respawn.
//
// Two things hurt anyone here: falling and explosions. Nothing fights back
// yet, so a mob that is hit takes it and dies.

const (
	maxHealth = 20

	// fallDamageMin is the drop a player walks away from, and each block past
	// it costs a point of health.
	fallDamageMin       = 3.0
	fallDamagePerBlock  = 1
	explosionDamageFull = 20.0

	// hurtInvulnTicks is how long a player is immune after being hurt. Without
	// it, standing in a blast would take a tick of damage per tick.
	hurtInvulnTicks = 10

	// attackReach is how far a player can hit something, in blocks.
	attackReach = 6.0
)

// damageTypeID is a damage type's registry id, which is what the Damage Event
// packet carries: the id is what picks the sound the client plays.
func damageTypeID(t proto.Tables, name string) int32 {
	return registryID(t.RegistryEntries["minecraft:damage_type"], name)
}

// deathMessages is the tail of what the server says when something kills a
// player.
var deathMessages = map[string]string{
	"minecraft:fall":          "fell from a high place",
	"minecraft:explosion":     "went off with a bang",
	"minecraft:player_attack": "was slain",
}

func deathMessage(name, cause string) string {
	if tail, ok := deathMessages[cause]; ok {
		return name + " " + tail
	}
	return name + " died"
}

// sendHealth draws the hearts. Health zero is what raises the death screen.
func (s *session) sendHealth() error {
	var w mcproto.Writer
	w.WriteFloat(s.health)
	w.WriteVarInt(20) // food: always full, there is no hunger yet
	w.WriteFloat(5)   // saturation
	return s.send(s.t.PlayClientboundSetHealth, w.Bytes())
}

// sendDamageEvent tells the client what just hurt it, which is what makes the
// hurt sound.
func (s *session) sendDamageEvent(t proto.Tables, cause string, source *Entity) {
	var w mcproto.Writer
	w.WriteVarInt(s.entityID)
	w.WriteVarInt(damageTypeID(t, cause))
	// Optional entity ids are sent as the id plus one, so zero means absent.
	if source != nil {
		w.WriteVarInt(source.ID + 1)
		w.WriteVarInt(source.ID + 1)
	} else {
		w.WriteVarInt(0)
		w.WriteVarInt(0)
	}
	w.WriteBool(false) // no source position
	_ = s.send(t.PlayClientboundDamageEvent, w.Bytes())
}

// hurtPlayer takes health off a player and, when that was the last of it, kills
// them.
func (s *session) hurtPlayer(t proto.Tables, amount float32, cause string, source *Entity) {
	if amount <= 0 || s.dead || s.hurtTicks > 0 || s.invulnerable {
		return
	}
	s.hurtTicks = hurtInvulnTicks
	s.health -= amount
	if s.health <= 0 {
		s.health = 0
	}
	s.sendDamageEvent(t, cause, source)
	s.srv.broadcastHurt(s.entityID, s.posX, s.posY, s.posZ, 0)
	s.srv.trackScore("health", s.name, int32(s.health+0.5))
	_ = s.sendHealth()
	if s.health <= 0 {
		s.die(t, cause)
	}
}

// tickHazards applies the damage the world does by itself: lava, and being on
// fire. hurtPlayer's own ten-tick invulnerability window paces both, which is
// vanilla's behaviour — lava hurts four every ten ticks while you stand in it,
// and burning hurts one a second. A player in creative is invulnerable, so this
// does nothing to them.
func (s *session) tickHazards() {
	if s.dead {
		return
	}
	if s.inLava() {
		// Vanilla's lavaHurt: four damage and fifteen seconds of fire.
		s.fireTicks = 300
		s.hurtPlayer(s.t, 4, "minecraft:lava", nil)
		return
	}
	if s.fireTicks > 0 {
		// Water puts you out. This is where a player who has been swimming in
		// lava finds out that the fire outlives the lava.
		if name, _, ok := s.srv.blockStateName(s.t, s.world.Block(int32(math.Floor(s.posX)), int32(math.Floor(s.posY)), int32(math.Floor(s.posZ)))); ok && name == "minecraft:water" {
			s.fireTicks = 0
			return
		}
		if s.fireTicks%20 == 0 {
			s.hurtPlayer(s.t, 1, "minecraft:in_fire", nil)
		}
		s.fireTicks--
	}
}

// inLava reports whether the block at the player's feet is lava. Vanilla tests
// the whole bounding box; feet alone is what matters for damage.
func (s *session) inLava() bool {
	name, _, ok := s.srv.blockStateName(s.t, s.world.Block(int32(math.Floor(s.posX)), int32(math.Floor(s.posY)), int32(math.Floor(s.posZ))))
	return ok && name == "minecraft:lava"
}

// die finishes a player off: the death screen with its message, a line in
// chat, and a player who stays down until they respawn.
func (s *session) die(t proto.Tables, cause string) {
	if s.dead {
		return
	}
	s.dead = true
	msg := deathMessage(s.name, cause)
	log.Printf("death: %s", msg)

	var w mcproto.Writer
	w.WriteVarInt(s.entityID)
	w.WriteBytes(nbtText(msg))
	_ = s.send(t.PlayClientboundPlayerCombatKill, w.Bytes())
	s.srv.broadcastSystemChat(msg)
	s.srv.trackScore("deaths", s.name, s.srv.bumpDeaths(s.name))
	s.srv.countStat(s.uuid, statCustom, statDeaths)

	// keepInventory off scatters the inventory where the player fell.
	if keep, _ := s.srv.rule("keepInventory"); !keep {
		for i := range s.inv {
			if s.inv[i].Empty() {
				continue
			}
			s.srv.spawnItemEntity(t, s.inv[i], s.posX, s.posY, s.posZ)
			s.inv[i] = world.Item{}
		}
		_ = s.sendInventory()
	}

	// doImmediateRespawn skips the death screen: the client is told not to
	// wait for a click, and the server puts the player back at once.
	if immediate, _ := s.srv.rule("doImmediateRespawn"); immediate {
		w.Reset()
		w.WriteByte(11) // game event: immediate respawn
		w.WriteFloat(0)
		_ = s.send(t.PlayClientboundGameEvent, w.Bytes())
		if err := s.respawn(); err != nil {
			log.Printf("immediate respawn failed: %v", err)
		}
	}
}

// respawn puts a dead player back on their feet at the spawn point.
func (s *session) respawn() error {
	s.dead = false
	s.health = maxHealth
	s.hurtTicks = 0
	s.falling = false
	s.onGround = true
	s.posX, s.posY, s.posZ = s.spawnX, s.spawnY, s.spawnZ
	s.fallFrom = s.posY

	var w mcproto.Writer
	s.writeSpawnInfo(&w)
	w.WriteByte(0) // nothing carried over: a respawn is a new body
	if err := s.send(s.t.PlayClientboundRespawn, w.Bytes()); err != nil {
		return err
	}
	s.teleportTo(s.posX, s.posY, s.posZ)
	return s.sendHealth()
}

// writeSpawnInfo is the dimension and game mode block that both the login
// packet and the respawn packet carry, which is why vanilla shares one record
// for it.
func (s *session) writeSpawnInfo(w *mcproto.Writer) {
	w.WriteVarInt(registryID(s.t.RegistryEntries["minecraft:dimension_type"], dimensionName))
	w.WriteString(dimensionName)
	w.WriteLong(0)            // hashed seed
	w.WriteVarInt(s.gameMode) // the player's own game mode
	w.WriteVarInt(0)          // previous game mode: undefined
	w.WriteBool(false)        // is debug
	w.WriteBool(true)         // is flat
	w.WriteBool(false)        // no death location to remember
	w.WriteVarInt(0)          // portal cooldown
	w.WriteVarInt(63)         // sea level
}

// trackFall watches the on-ground flag and charges for the drop when a player
// lands. The client reports the flag; the server keeps the height.
func (s *session) trackFall() {
	if s.dead {
		return
	}
	switch {
	case !s.onGround && !s.falling:
		s.falling, s.fallFrom = true, s.posY
	case !s.onGround && s.posY > s.fallFrom:
		s.fallFrom = s.posY
	case s.onGround && s.falling:
		s.falling = false
		if drop := s.fallFrom - s.posY; drop > fallDamageMin {
			damage := float32((drop - fallDamageMin) * fallDamagePerBlock)
			s.hurtPlayer(s.t, damage, "minecraft:fall", nil)
		}
	}
}

// Entity event ids that mean something to a living thing.
const (
	entityEventHurt  = 2
	entityEventDeath = 3
)

// handleClientCommand is what the client sends after it has been asked to do
// something: in practice, that it has clicked respawn.
func (s *session) handleClientCommand(r *mcproto.Reader) {
	action, err := r.ReadVarInt()
	if err != nil {
		return
	}
	const (
		performRespawn = 0
		requestStats   = 1
	)
	if action == requestStats {
		// The client's statistics screen asking for its numbers.
		s.sendStatistics()
		return
	}
	if action != performRespawn || !s.dead {
		return
	}
	if err := s.respawn(); err != nil {
		log.Printf("respawn failed: %v", err)
	}
}

// broadcastHurt plays the hurt animation for one entity for everyone, and the
// entity event that carries a living thing flinching.
func (s *Server) broadcastHurt(entityID int32, x, y, z float64, yaw float32) {
	var w mcproto.Writer
	w.WriteVarInt(entityID)
	w.WriteFloat(yaw)
	body := w.Bytes()
	s.viewersAt(x, y, z, func(sess *session) {
		if sess.t.PlayClientboundHurtAnimation != 0 {
			_ = sess.sendQuiet(sess.t.PlayClientboundHurtAnimation, body)
		}
	})
	s.broadcastEntityEvent(entityID, x, y, z, entityEventHurt)
}

// broadcastEntityEvent sends an entity event (hurt, death, and the rest) to
// everyone.
func (s *Server) broadcastEntityEvent(entityID int32, x, y, z float64, event byte) {
	var w mcproto.Writer
	w.WriteInt(entityID) // an int, not a varint
	w.WriteByte(event)
	body := w.Bytes()
	s.viewersAt(x, y, z, func(sess *session) {
		if sess.t.PlayClientboundEntityEvent == 0 {
			return
		}
		_ = sess.sendQuiet(sess.t.PlayClientboundEntityEvent, body)
	})
}

// systemChatBody is a system chat message: the text, then whether it is an
// overlay (action bar) message rather than one for the chat window. Leaving
// the flag off is a packet the client cannot decode, which is what a death
// message used to be.
func systemChatBody(msg string, overlay bool) []byte {
	var w mcproto.Writer
	w.WriteBytes(nbtText(msg))
	w.WriteBool(overlay)
	return w.Bytes()
}

// broadcastSystemChat says something to everyone with no sender attached,
// which is what death messages and the like are.
func (s *Server) broadcastSystemChat(msg string) {
	body := systemChatBody(msg, false)
	for _, sess := range s.sessionList() {
		_ = sess.sendQuiet(sess.t.PlayClientboundSystemChat, body)
	}
}

// handleAttack is a left click on an entity.
func (s *session) handleAttack(r *mcproto.Reader) {
	id, err := r.ReadVarInt()
	if err != nil {
		return
	}
	e := s.srv.entityByID(id)
	if e == nil {
		return
	}
	dx, dy, dz := e.X-s.posX, e.Y-s.posY, e.Z-s.posZ
	if dx*dx+dy*dy+dz*dz > attackReach*attackReach {
		return
	}
	switch e.Kind {
	case kindMob:
		s.srv.hurtMob(s.t, e, s.attackDamage(), s)
	default:
		// Items on the ground and primed TNT are not something to hit.
	}
}

// attackDamage is what the held item does, from vanilla's numbers for the
// weapons a creative player is likely to swing. Everything else is a fist.
func (s *session) attackDamage() float32 {
	name, ok := s.t.ItemName[s.mainHandItem().ID]
	if !ok {
		return 1
	}
	switch name {
	case "minecraft:netherite_sword":
		return 8
	case "minecraft:diamond_sword":
		return 7
	case "minecraft:iron_sword":
		return 6
	case "minecraft:stone_sword":
		return 5
	case "minecraft:wooden_sword", "minecraft:golden_sword":
		return 4
	case "minecraft:netherite_axe":
		return 10
	case "minecraft:diamond_axe":
		return 9
	case "minecraft:iron_axe":
		return 9
	case "minecraft:stone_axe":
		return 9
	case "minecraft:wooden_axe", "minecraft:golden_axe":
		return 7
	}
	return 1
}

// mobHealth is how much a mob can take. Vanilla reads these from each entity's
// attributes, which this server does not carry.
var mobHealth = map[string]float32{
	"minecraft:zombie":      20,
	"minecraft:husk":        20,
	"minecraft:drowned":     20,
	"minecraft:skeleton":    20,
	"minecraft:stray":       20,
	"minecraft:creeper":     20,
	"minecraft:spider":      16,
	"minecraft:cave_spider": 12,
	"minecraft:cow":         10,
	"minecraft:pig":         10,
	"minecraft:sheep":       8,
	"minecraft:chicken":     4,
	"minecraft:wolf":        8,
	"minecraft:cat":         10,
	"minecraft:horse":       15,
	"minecraft:donkey":      15,
	"minecraft:villager":    20,
	"minecraft:iron_golem":  100,
	"minecraft:snow_golem":  4,
}

// mobDrops is what a mob leaves behind, one entry per stack. Vanilla reads
// this from loot tables.
var mobDrops = map[string][]struct {
	Item  string
	Count int32
}{
	"minecraft:zombie":   {{"minecraft:rotten_flesh", 1}},
	"minecraft:husk":     {{"minecraft:rotten_flesh", 1}},
	"minecraft:drowned":  {{"minecraft:rotten_flesh", 1}},
	"minecraft:skeleton": {{"minecraft:bone", 1}, {"minecraft:arrow", 2}},
	"minecraft:stray":    {{"minecraft:bone", 1}, {"minecraft:arrow", 2}},
	"minecraft:creeper":  {{"minecraft:gunpowder", 1}},
	"minecraft:spider":   {{"minecraft:string", 1}},
	"minecraft:cow":      {{"minecraft:leather", 1}, {"minecraft:beef", 1}},
	"minecraft:pig":      {{"minecraft:porkchop", 1}},
	"minecraft:sheep":    {{"minecraft:mutton", 1}, {"minecraft:white_wool", 1}},
	"minecraft:chicken":  {{"minecraft:feather", 1}, {"minecraft:chicken", 1}},
}

// mobHealthFor is a mob's starting health.
func mobHealthFor(t proto.Tables, typeID int32) float32 {
	if h, ok := mobHealth[proto.EntityTypeName(t, typeID)]; ok {
		return h
	}
	return maxHealth
}

// hurtMob takes health off a mob and, when it runs out, kills it and drops
// what it was carrying.
func (s *Server) hurtMob(t proto.Tables, e *Entity, amount float32, attacker *session) {
	if amount <= 0 || e.HurtTicks > 0 {
		return
	}
	if e.Health <= 0 {
		e.Health = mobHealthFor(t, e.Type)
	}
	e.HurtTicks = hurtInvulnTicks
	e.Health -= amount
	s.broadcastHurt(e.ID, e.X, e.Y, e.Z, 0)
	// The mob's own hurt sound, which is also what tells the player the hit
	// landed.
	s.playMobSoundFrom(t, proto.EntityTypeName(t, e.Type), "hurt", e.ID, e.X, e.Y, e.Z, 1, 1)
	// The hit knocks the mob back, away from whoever hit it. The server's own
	// velocity changes and the clients watching are told, rather than the client
	// working the motion out from the damage packet.
	if attacker != nil {
		dx, dz := e.X-attacker.posX, e.Z-attacker.posZ
		if length := math.Hypot(dx, dz); length > 1e-4 {
			s.knockback(t, e, dx/length*0.4, 0.4, dz/length*0.4)
		}
	}
	if e.Health > 0 {
		return
	}

	s.broadcastEntityEvent(e.ID, e.X, e.Y, e.Z, entityEventDeath)
	s.playMobSoundFrom(t, proto.EntityTypeName(t, e.Type), "death", e.ID, e.X, e.Y, e.Z, 1, 1)
	s.removeEntity(e.ID)
	if loot, _ := s.rule("doMobLoot"); !loot {
		return
	}
	for _, drop := range mobDrops[proto.EntityTypeName(t, e.Type)] {
		id, ok := t.ItemIDs[drop.Item]
		if !ok {
			continue
		}
		s.spawnItemEntity(t, world.Item{ID: id, Count: drop.Count}, e.X, e.Y, e.Z)
	}
}

// damageInBlast hurts everything standing in an explosion, players and mobs
// alike.
func (s *Server) damageInBlast(t proto.Tables, x, y, z, power float64) {
	for _, sess := range s.sessionList() {
		d := math.Sqrt(sq(sess.posX-x) + sq(sess.posY-y) + sq(sess.posZ-z))
		if d > power {
			continue
		}
		sess.hurtPlayer(t, float32(explosionDamageFull*(1-d/power)), "minecraft:explosion", nil)
	}
	for _, e := range s.entities() {
		if e.Kind != kindMob {
			continue
		}
		d := math.Sqrt(sq(e.X-x) + sq(e.Y-y) + sq(e.Z-z))
		if d > power {
			continue
		}
		s.hurtMob(t, e, float32(explosionDamageFull*(1-d/power)), nil)
	}
}

func sq(v float64) float64 { return v * v }
