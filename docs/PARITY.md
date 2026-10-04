# Parity with the vanilla server

What this server implements, what it approximates, and what is missing, measured
against the official 26.3 server. "Vanilla" below always means the behaviour of
that server as read from its source, which is the reference for every rule here.

The point of this file is to keep the gaps honest and ordered. Anything marked
*approximated* has a known difference in behaviour, written out rather than
hidden.

## Status at a glance

| Subsystem | State |
|---|---|
| Protocol (login, configuration, play) | implemented for 1.21.8, 26.2, 26.3 |
| Chunks and light | implemented (sky light approximated) |
| Block placement and breaking | implemented (creative); per-state hardness dumped from the game, and unbreakable blocks refused |
| Containers and menus | chests, barrels, hoppers, shulkers, dispensers, furnaces, crafting, anvils, lecterns (partly) |
| Item stacks and components | components carried verbatim, custom names interpreted |
| Entities | players, mobs, dropped items and TNT; no AI, so nothing fights back — but a hit does knock a mob back |
| Health and death | health, fall and blast damage, death screen, respawn |
| Fluids | both spread, react, burn, and carry entities |
| Commands | vanilla's own tree; a handful of commands run |
| Everything else | see below |

## World

**Implemented**
- Flat world and serving a saved world's region files, with local edits on top.
- Per-chunk streaming with the batch protocol, view distance, forgetting chunks.
- Block entities in streamed chunks for all 49 families (chests, signs, banners,
  beds, lecterns, shulkers, ...). Without this the client draws nothing there.
- Sky light and block light. Block light is a real engine: emitters, propagation,
  vanilla's decrease pass for removal, and per-section arrays in chunk payloads
  and light updates.
- Water: sources spread and fall, falling columns keep to themselves until they
  land, and a pool drains when its source is taken away. Lava spreads too, on
  its own slower tick, and the two set into obsidian, cobblestone or stone where
  they meet.

**Approximated**
- **Sky light** is computed per column (lit above the topmost block) rather than
  by a full sky-light engine. Correct for open ground and under a roof; a
  light-transparent block in a column is treated as the top.
- **Opacity** comes from a written-out table, not per-state shapes, so a few
  blocks that let light through (some decorative blocks) block it. Erosion is
  always toward less light, never more.
- **Emission** is a table of the emitters a player meets, not every block in the
  game.
- **Weather** is a state the clients are told about; rain does not fall, and there
  are no lightning or snow.
- **World generation** does not exist: the flat world, or a saved world's chunks.

## Chat

**Implemented**
- Player messages go out as **Disguised Chat**: a player message with no
  signature, which is the right shape for a server that does not do signed chat.
  Chat is broadcast to everyone, the sender included.

**Deliberate simplification**
- The **signed chat** protocol is not implemented, and that is a choice rather
  than a gap: this server sends unsigned chat, so there is nothing to verify and
  nothing to acknowledge. The client's chat session update and chat ack are
  **tolerated** — a client that sends them is not disconnected, and talking
  afterwards works. Signed chat would mean key verification, per-message
  signatures and the "not delivered" accounting, for no gain on a server that
  does not authenticate its players.

## Blocks

**Implemented (interaction)**
- **Doors, trapdoors and fence gates open and close** on a right click, both
  halves of a door together, with the sound the block makes. A door opens
  whatever is in hand, including an empty hand.
- **Chests, ender chests and shulker boxes swing their lids**: the client
  animates from a **block event** — the position, b0 = 1, b1 = the number of
  players who have it open, then the block's registry id — and the count is kept
  per position, so one player closing a chest that two are using does not shut
  it. A **barrel** is the odd one out: it has no block event, it flips its
  `OPEN` block state, and the world records that so a chunk resent later keeps
  the lid open. The creak is a separate sound packet, as in vanilla.
- **Bells ring when punched**: the hit sends the block event carrying the face
  struck — which is what the client swings the bell from — plus the bell's hit
  sound. The break path used to break the bell instead, so it could never ring.
- **An eye of ender fills an end portal frame**: the eye is part of the frame's
  block state, so the client draws it from the block update, and the sound is the
  frame's own. The eye is spent in survival. Nothing could trigger a frame
  before, so an eye of ender did nothing at all.
- **Breaking uses the right action id**: the player-action packet's action is
  `0` start, `1` change direction, **`2` abort**, **`3` stop** — and 3, not 2, is
  the one that breaks the block. Reading 2 as "finished" meant a dig only ever
  broke anything when it was *aborted*, so breaking never actually worked. The
  client's own prediction hid it: the block looked gone until that prediction was
  dropped — by an ack or a chunk resend — which is the "the blocks I removed came
  back" report. Once the ack for an aborted dig started being sent, the same bug
  turned into "it will not let me remove stuff".
- **A dig is timed from the block's hardness and the tool in hand**: progress
  per tick is `toolSpeed / hardness / (correctTool ? 30 : 100)`, vanilla's
  `BlockBehaviour.getDestroyProgress`, with the tool's class from the
  `mineable/*` block tags and its material from `incorrect_for_<material>_tool`
  — so stone is six ticks with a diamond pickaxe, twenty-three with a wooden one
  and a hundred and fifty by hand, and an iron pickaxe is *slow* on obsidian
  rather than diamond-fast. From that clock the server sends the **cracking
  stages** to everyone else as they advance (`destroyBlockProgress`) and the
  dig's sound every tick — 2019, and the lower variant 2020 every fourth, as
  vanilla alternates them. Before this a dig showed one stage and had it wiped,
  which reads as a block vanishing with no warning.
- **A block that needs the right tool drops only for that tool.** Stone mined
  with a bare hand still breaks and gives nothing; this server used to hand out
  cobblestone for a fist.
- **What is *not* enforced: how long the dig took.** Vanilla's server can refuse
  a STOP that arrives before the block's own time, but it works that out from
  the player's enchantments and effects, and this server tracks neither — so the
  same check here would refuse a legitimate Efficiency V dig and drop the
  client's prediction of it, which is exactly the ghosting just cured. The
  client's STOP stays what breaks the block, and the model above is used only
  where being slow costs nothing (stages, sounds) or where being wrong is
  visible (drops).
- **Every block action is acknowledged**, including the ones that change nothing
  — opening a container, a door or a lectern, ringing a bell, an empty hand, an
  unbreakable block. The ack lands when the handler finishes, so it follows any
  block update: an ack *before* the update makes the client roll back for a frame
  (the placement flicker), and never acking at all leaves the client holding its
  prediction forever — a ghost block that reappears whenever the chunk is sent
  again, which reads as a block you removed coming back. The server logs that
  sequence: every player-driven block change, and every chunk it sends that
  carries player edits (with a hash of the bytes and the edit count). Read it
  with one fact in mind — **nothing re-sends a chunk because a block changed**;
  a chunk goes out when it is first streamed, or after it was forgotten and came
  back into range (`TestARevisitedChunkIsSentWithTheEdits`). So if a ghost turns
  up mid-session with no chunk line for that chunk in the log, the chunk was
  never re-sent and the payload is not the culprit: it is the prediction and ack
  path.
- **Breaking a block takes its block entity with it.** A sign's text and a
  banner's patterns live in this server's own stores rather than in the world
  file, and the break path never cleared them — the sign store did not even have
  a delete. So a sign's text outlived its block, a sign placed there later
  inherited the old text, and a chunk listed a block entity for a block that was
  gone. All three stores are cleared on break now, and a chunk skips a sign entry
  whose block is no longer a sign.
- `/setblock <x> <y> <z> <block>[property=value,...]`, with the properties
  looked up in the generated state table.
- **Lecterns take a book and show it**: a written or writable book in hand goes
  onto the lectern (one off the stack — creative keeps it, as vanilla's
  `consumeAndReturn` does — and the block's `has_book` property turns on so the
  client draws it), using it again opens the book at page zero, the page buttons
  turn and clamp at the last page (read from the book's content component), and
  the take button puts the book back in the player's inventory, or drops it at
  the lectern when there is no room. `item.book.put` plays as it lands.

**Approximations (interaction)**
- The lectern's page is **per viewer**: vanilla keeps it in the block entity, so
  two players looking at one lectern see the same page and it survives a
  restart. Here it starts at the first page every time the screen opens.
- The book is recognised by its item name rather than by its content component;
  for the two book items that amounts to the same thing.
- Two doors side by side do not open together: the pairing is a shape rule of
  its own and is not implemented.
- Doors do not need power and cannot be locked: there is no redstone, so a
  lever, a button and an iron door's need for power are all absent.

## Statistics

**Implemented**
- The client's statistics screen is filled from one packet, and this server
  answers the client's request for it. Counted: **blocks mined** by block,
  **deaths**, and **play time**.

**Approximations**
- The counters are **per player**, as vanilla's are — one player's mining is not
  on another's screen. Play time is measured from when that player joined
  rather than being ticked, which comes to the same thing.
- Everything else a statistics screen lists (crafted, used, killed, walked, and
  the rest) is not counted at all. Only what is listed above is, on the
  principle that a number the server does not keep is worse than a missing one.

## Boss bars

**Implemented**
- Boss bars with a name, a progress, a colour and a style, and
  `/bossbar add|remove|set|list`. They are sent to everyone on change and to a
  player as they join.

**Missing**
- **Changing a bar's name** after creating it: the packet has an operation for
  it whose encoding was not confirmed against the decompiled source, and this
  server does not send a packet it has not read.
- The flags that darken the screen, play boss music and add fog: they are sent
  as zero, and nothing sets them.

## Scoreboard

**Implemented**
- Objectives with a name, a display name and a criterion, scores per holder, and
  the **sidebar** display: `/scoreboard objectives add|remove|setdisplay` and
  `/scoreboard players set`. A joining player is sent the objectives, their
  scores and the sidebar.
- The criteria the server can see are counted: `health` (updated as a player is
  hurt) and `deaths`, plus `dummy`, which only changes when a command says so. A
  criterion the server cannot count is refused rather than showing a number that
  never moves.

**Missing**
- Teams, the other display slots (`list`, `below_name`) and the number formats
  (the hearts display is sent as a render type but nothing sets it yet).
- Deaths are counted per session: this server does not remember a player's
  scores between sessions.

## Fluids

**Implemented**
- **Water and lava flow one cell at a time, the way `FlowingFluid` does.** A cell
  that something touched recomputes its own level from its **neighbours**
  (`getNewLiquid`: the highest neighbour's level one thinner; falling when the
  same fluid sits above it; and a **source** when two or more source neighbours
  sit over solid ground), and then spreads — down first, and sideways only when
  it cannot fall or is a source. That replaced a whole-pool breadth-first reflow
  with a "front" counter. The levels are neighbour-derived like vanilla's, so a
  pool settles into its own shape, a cell fed by two flows takes the closer one,
  and the result no longer depends on map iteration order (which it did: the old
  reflow walked Go maps, and a fluid test flaked on it).
- **Two adjacent sources fill the block between them**, which is what makes a
  pool infinite; take a source away and the flow thins and recedes a step at a
  time rather than vanishing at once.
- Levels are stored the way the block state reads — 0 for a source, 1-7 for
  flowing water, 8 for a falling column — and mapped to vanilla's *amount* where
  the arithmetic needs it, so a falling cell counts as a full one. Getting that
  wrong is what stopped a landed column from flooding: the falling neighbour
  read as empty.
- A falling column keeps to itself until it lands, and then floods outwards.

- **Lava spreads too**, three blocks from its source — its drop-off is two levels
  a step to water's one — and on its own much slower tick, every thirty ticks to
  water's five. The two fluids keep separate queues of positions to re-check,
  because one queue shared between them starves whichever ticks more slowly.
- **Water and lava react, and which of the three solids appears is decided by
  which fluid reacts**: the block that reacts is the **lava**, on the five sides
  up, north, south, east and west (`LiquidBlock.shouldSpreadLiquid`) — obsidian
  when it is a **source**, cobblestone when it **flows** — while water **under**
  lava is what a fall sets into **stone** (`LavaFluid.spreadTo`), the one
  direction the two fluids trade places at all. Every reaction plays vanilla's
  fizz, level event 1501, where it happens.
  - The reaction fires on the **block change**, the way
    `LiquidBlock.neighborChanged` does, so pouring a bucket beside the other
    fluid reacts at once instead of waiting for a fluid tick that may never
    reach it.
  - These rules were written, removed, and brought back, and the reason they
    are stable now is a rule that came out of the failure: **a fluid may only
    write over air or its own kind**, so a solid either pass leaves behind is
    never undone by the other. Without that, the outcome flipped between stone,
    cobblestone and lava depending on which pass ran first.
  - The **spread itself is ported from `FlowingFluid`**: `getSpread` and
  `getSlopeDistance` are followed closely, so water heads for the **nearest
  drop** — searched up to the fluid's slope find distance, four blocks for water
  and two for lava — and spreads only in those directions. That is what makes
  water flow downhill instead of ponding outwards, and it is the difference from
  a plain distance-based flood. `getDropOff` (one level per step) and the
  falling rule (a column spreads at full strength) come from the same source.
- **Lava burns you.** Standing in it hurts four every ten ticks — the pacing is
  `hurtPlayer`'s own ten-tick invulnerability window, the same one vanilla uses —
  and lights you on fire for fifteen seconds. Burning hurts one a second, and
  water puts it out. Being in creative still means nothing touches you.
- **A current carries entities**: water and lava push whatever is in them along
  the flow, ported from `EntityFluidInteraction` and `FlowingFluid.getFlow`. The
  flow of the block at an entity's feet is `thisHeight - neighbourHeight` per
  side, which is what makes it point downhill; a fluid shallower than four
  tenths of a block pushes proportionally less; and the impulse is the fluid's
  current strength (water 0.014, lava 0.00233). Two approximations: the falling
  case pushes down without vanilla's test for a solid face, and **players are
  not pushed**, since their movement is the client's.
- **Not ported yet**: fire does not spread between blocks, and vanilla's fluid
  *scheduling* — a tick queued per position with its own delay — is approximated
  by one pass per fluid tick over the cells something touched.

**Implemented (light)**
- The light engine follows vanilla's step cost, `LightEngine.getOpacity`:
  a step costs `max(1, dampening)`. Light falls by one through air and by two
  through a state that dampens it — water and lava so far — instead of
  everything that is not air blocking it outright.

**Implemented (light, the dampening table)**
- **The per-state dampening table is generated by running the game**, not
  guessed. `tools/dump_light/DumpLight.java` boots the server jar and writes
  `BlockBehaviour.BlockStateBase.getLightDampening()` for every state, and
  `tools/gen_state.py` packs it into one byte per state id — 35723 values for
  26.3, 32366 for 26.2. Vanilla *computes* dampening from per-state shapes (15
  when the occlusion shape is a full block, otherwise `propagatesSkylightDown()
  ? 0 : 1`), so the block report cannot carry it; the note here used to claim the
  table was unnecessary and that the report was enough, and it was not.
  What it buys: light now falls by one through **leaves, cobwebs and water** — it
  used to stop dead at a tree — while stone and tinted glass stop it and glass
  passes it unchanged.
- **The emission table is dumped the same way**, and it replaced a hand-written
  map that was measured against the dump: of 35723 states that map was **missing
  291 emitters** (lit furnaces, redstone ore's lit state, 26.3's copper torches),
  **spurious on 58** and at the **wrong level on 47** — which is what a light
  source that does not light looks like.

- **Light removal is vanilla's decrease pass**: `BlockLightEngine.checkNode`
  decides between taking light away and pulling it in, every decrease runs
  before any increase, and each entry on the queue carries the level *and the
  directions it may still propagate in* (`decreaseSkipOneDirection`,
  `increaseOnlyOneDirection`, and `PULL_LIGHT_IN_ENTRY` for a cell that has
  opened up). The directions are the part the first attempt at this port left
  out — a queue of (position, level) pairs re-lit the hole from the wrong side
  and left a gradient standing where the light had been.
  - What it is verified by: a bright light's removal takes its own field with
    it; a dimmer source inside that field keeps its light and its shape; removal
    with dampening in the way clears every cell of the shorter reach, including
    the edge; and light comes back **into** the space a broken block left, which
    the clear-the-reach removal could not do at all
    (`TestLightFlowsIntoAClearedBlock`).

**Missing (light)**
- **1.21.8's dampening table is projected, not dumped.** Mojang's bundler ships
  the 26.x servers with *named* classes and 1.21.8 with obfuscated ones — checked
  by opening both inner jars: 26.2 has
  `net/minecraft/world/level/block/state/BlockBehaviour`, 1.21.8 has no
  `net/minecraft` classes at all — so `DumpLight`, which reads
  `getLightDampening`, cannot run against it without the official mappings and a
  remapper, and this machine has neither. The block report cannot answer it
  either: neither 1.21.8's nor 26.3's carries a light field. So
  `tools/project_light.py` matches each 1.21.8 state to the 26.3 state with the
  same block and properties and takes that state's dampening — **27940 of 27946
  states**, with `minecraft:chain`'s six written as `0xff` and left to the
  opacity rule. A projection by name, not the game's own numbers: the same
  technique the fuel tables use for these two versions. Getting the donor and
  target the wrong way round is silent and poisonous — the first run produced a
  table indexed by 26.3's ids, which is why the script now says which is which
  and why the checks below are in place.
- Emitters used to be **excluded** from the opaque set, which let light pass
  through glowstone; vanilla's rule makes a full-cube emitter cost 15 like any
  other solid block. Removed. An emitter still lights its neighbours, because
  the step is charged for the block being stepped *into*: 15 minus air's one is
  fourteen.

**Approximations**
- Lava burns a player but does not **ignite** the world: a lava block setting
  fire to the blocks beside it, and fire spreading, are their own piece of work.
- The fluids are **not ticked on vanilla's per-position schedule**: this server
  runs one pass per fluid tick over the cells that something touched, rather
  than a queued tick with its own delay per position. The pacing is the same;
  the bookkeeping is not.

## Blocks

**Implemented**
- Placement follows vanilla's `getStateForPlacement` per block: horizontal facing
  (with the anvil's clockwise rule), pillar axis, stairs and slab halves, trapdoors,
  doors as two blocks with a hinge, beds as two blocks, lantern hanging, wall
  variants for signs, banners and torches, chest pairing into double chests.
- Breaking: timed from the block's hardness and the tool in hand, with the
  cracking stages and the dig's sound for everyone else, the correct-tool drop
  rule, and block updates broadcast to everyone in range.
- Furnaces, blast furnaces and smokers cook with real recipes, fuel and burn
  times, the lit block state, and progress properties.
- Water flow; buckets place and pick up water.

**Missing**
- **Block ticks**: crops growing, grass spreading, fire, ice and snow forming,
  leaf decay, saplings, vines. Nothing in the world changes on its own except
  fluids and furnaces.
- **Redstone**: wire, torches, repeaters, pistons, observers, and everything
  built on them.
- **Fluid mixing**, lava spread, waterlogging on placement.
- **Explosions** other than primed TNT (beds, respawn anchors, crystals).
- Block-specific behaviour on use: note blocks, jukeboxes, cauldrons, composters,
  beacons, bells, cake, and the rest.
- Breaking fidelity beyond creative: hardness, tool speed, destroy stages.

## Entities

**Implemented**
- Players: spawn, despawn, movement, metadata (sneak/sprint flags, skin layers),
  held items, arm swings, tab list.
- A simple entity store with gravity, ground collision and movement broadcast.
- Primed TNT with a fuse, and an explosion that removes blocks and tells clients.
- Spawn eggs create the mob they name.
- Mobs wander: they stand for a while, walk in a direction for a while, turn
  when a block is in the way, and stop and face a player who comes within eight
  blocks. Their walking and looking are sent to clients.
- Entity packets go only to the players who can see the entity, and a player
  joining an active world is told about the mobs and items already around them.
- Dropped items: they fall, land, merge with a like stack beside them, are drawn
  from the stack sent in their metadata, despawn after five minutes, and are
  taken by a player standing within a block and a half. An explosion leaves
  behind what it broke, up to sixty-four items per blast.
- **Thrown snowballs, eggs and ender pearls**: the right click with nothing under
  it throws what is in hand along the look vector at 1.5 blocks a tick, on
  `ThrowableProjectile`'s physics — gravity 0.03, drag 0.99, and a swept move so
  a fast throw cannot pass through a wall. The packet for it, `use_item`, was
  the one serverbound packet this server did not route at all. A snowball or an
  egg bursts where it hits, drawn from **entity event 3**, which is the puff of
  particles and its sound in one packet — and the only particles this server
  sends. An ender pearl carries its thrower to where it landed and costs them
  five damage in survival; the thrower is ignored for the throw's first two
  ticks, so a level throw does not end where it began.

**Approximations**
- A pickup is a **distance test**, not the player's box touching the item's.
- Explosion drops are **every block that has an item**, capped at sixty-four,
  rather than vanilla's per-block rules.
- Items combine on the ground only at the moment one lands on another, not over
  time.
- A projectile collides as a **point against a distance test** rather than box
  against box, and it does not slow in water (`ThrowableProjectile` drags at 0.8
  there). A thrown thing has no lifetime either: vanilla's has none, so one that
  never hits anything flies until it leaves the world and is dropped at the
  bottom of it.
- A mob's walk is a **random direction on a timer**, not a path or a goal; its
  speed is one number for every mob rather than per-mob movement attributes.
- **Entity tracking is a distance** (64 blocks), not vanilla's per-chunk
  tracking. The radius matches the chunk radius, so anything in it is in a
  chunk the client has been sent.

**Missing**
- **Mob AI**: pathfinding and goals. A mob here wanders, looks at players and
  turns at walls, but it does not path around anything, chase, or attack, and
  it never despawns or breeds.
- **Damage and health**: nothing can be hurt, so nothing dies.
- A player dropping an item from the inventory (the Q key).
- Projectiles: arrows, snowballs, ender pearls, fireballs.
- Vehicles: boats, minecarts, and the rails under them.
- Entity tracking per view distance: entity packets go to every player rather
  than to those who can see them.
- Most entity metadata, equipment for non-players, passengers, attributes.

## Health and death

**Implemented**
- Game rules the server acts on, with `/gamerule <name> [true|false]`:
  `doDaylightCycle` (the clock holds still, and `/time` still moves it),
  `doTileDrops` and `doMobLoot` (what breaking blocks and killing mobs leaves
  behind), `keepInventory` (off scatters the inventory where the player fell),
  and `doImmediateRespawn` (the player is put back at once instead of being
  asked to click). Only rules the server actually keeps are listed: agreeing to
  one it did not implement would be a lie.

**Approximations (game rules)**
- The rule values live in the **server**, not in the data-pack NBT the client
  receives, so the client's own gamerule screen goes on showing the defaults it
  was sent. Rewriting that blob is what it would take to keep it in step.

- Game modes per player, creative and survival. `/gamemode` changes the mode,
  the abilities packet follows it (creative is invulnerable, can fly and breaks
  instantly), and so does the login and respawn's spawn info. Only creative
  players are immune to damage, so switching to survival makes falling and
  explosions hurt, which is what makes dying reachable at all.
- Players have health, sent to the client whenever it changes; zero is what
  raises the death screen.
- Fall damage: the client reports when it is on the ground, the server remembers
  the height the fall started from, and a drop of more than three blocks costs a
  point per block past that.
- Explosions hurt everything in the blast, players and mobs alike.
- A mob can be hit (fist or a weapon's damage) and killed; it then leaves its
  drops as item entities. Death plays the hurt and death animations for anyone
  watching.
- Death sends the death message (the screen and chat), and the client's respawn
  click puts the player back at the spawn point with full health.

**Approximations**
- **Attack damage** comes from a small table for the common swords and axes,
  not from item attributes; a fist is one point.
- **Mob health and drops** are tables in the server, not the entities'
  attributes or loot tables.
- **Explosion damage** is linear with distance from the middle, without the
  per-block visibility test vanilla does.
- There is **no natural regeneration and no hunger**: food is always full, and
  health only comes back by respawning.
- **Breaking is instant in both modes**: survival has no block hardness or tool
  speed yet, so the only difference between the modes is damage and flight.
- The hurt animation always faces yaw zero rather than the attacker.

**Implemented (sound)**
- Breaking and placing a block plays its sound for everyone nearby, and
  breaking shows the break particles.
- Mobs make a **footstep** every block and a half, taken from the block under
  their feet, so a mob crossing a room is heard. A player's own footsteps are
  the client's business; these are for everyone else.
- Containers creak: chests, trapped chests, barrels, shulker boxes and ender
  chests have their open and close sounds, taken from the block that was opened,
  so a barrel does not sound like a chest. A furnace or crafting table has no
  such sound and stays quiet, as in vanilla.
- Mobs make their own noise: their hurt and death sounds when they are hit and
  killed, and an ambient one now and then on a staggered timer. A mob with no
  sound of its own falls back to the generic one, and an action with no generic
  counterpart plays nothing, as vanilla's do.
- Explosions send one Explode packet: the burst, the bang, and the smoke. The
  blast changes the world in one pass and then sends the block updates and one
  light update per affected chunk, rather than a light update per block: that
  storm was what made the client re-mesh over and over and the blast look like
  it was eating the ground row by row.

**Approximations (sound)**
- A block's sound group is **guessed from its name** (wood, glass, wool, sand,
  gravel, grass, stone), because the block table does not carry vanilla's sound
  types.

**Implemented (breaking)**
- Breaking a block in survival drops what it should: stone gives cobblestone,
  ores give their raw material, glass gives nothing, and a block not named in
  the table gives itself. Creative leaves nothing, which is what the mode is
  for. Drops land as item entities, so they are picked up the usual way.
- While someone digs, everyone else nearby sees the block's cracking texture,
  and it is cleared when the block goes or the dig is aborted.

**Approximations (breaking)**
- The drop table is **hand-written** for the blocks a player actually breaks;
  vanilla reads a loot table per block. Silk touch, fortune and the rest of the
  tool rules are not there.
- The cracking stages are **not timed**: the client doing the digging draws its
  own animation from the block's hardness, and the broadcast only tells
  onlookers that a block is being broken and when to stop.
- **Hardness and tool speed are the client's**: it decides how long a block
  takes and tells the server when it is done, and the server agrees. A modified
  client could break anything instantly.

**Missing**
- Mobs that fight back, projectiles, armour, enchantments, potion effects.
- The death message does not name the killer (there are no killers yet).
- Fall damage does not consider water or other softening blocks.

## Items

**Implemented**
- Stack identity includes components; anything the client sends in the creative
  inventory is captured and echoed back verbatim, so a named or enchanted item
  survives a container.
- Custom names (the anvil's rename), with the anvil's own rules for validity.
- **Banner patterns** — the first component anything *read* (a lectern book's
  page count is the second, and only the count). A banner's layers
  are not in its block state: they arrive as the item's banner_patterns
  component. That component is BannerPatternLayers and the block entity's
  patterns field is BannerPatternLayers too, so the value passes through
  untouched — the component's bytes are the field's bytes, and the block entity
  carries them in the chunk list, in Block Entity Data and across a restart.
  Re-encoding it could only get it wrong.
- Buckets (water, lava), spawn eggs, flint and steel on TNT, middle-click pick.

**Missing**
- **Component semantics**: only banner patterns and a lectern book's page count
  are read (above). Nothing reads enchantments, potions, food, durability or
  shulker contents — the values are carried, but nothing acts on them.
- Item use in general: eating, potions, ender pearls, bows, shields, buckets for
  picking up fluid from the world.
- Crafting remainders beyond the five Item.Properties entries (water/lava/milk
  bucket, dragon's breath, honey bottle).
- The recipe book, and recipes beyond crafting and cooking (stonecutting,
  smithing, brewing).

## Player

**Implemented**
- Creative only: flight, instant breaking, no hunger and no damage.
- Inventory, hotbar, held item sync, spawn point on join, persistence.
- Chat (unsigned), commands, the tab list, difficulty, weather and time states.

**Missing**
- Survival: health, hunger, experience, damage sources, death and respawn.
- Permissions and operators, spawn protection, whitelist and ban lists.
- Scoreboard, teams, advancements, statistics, boss bars.
- Sleep, and beds as respawn points.

## Commands

**Implemented**: vanilla's whole command tree, byte for byte. `tools/gen_commands.py`
reads the data pack's `reports/commands.json` — 2475 nodes across 59 argument
types — and emits the exact body of the Commands packet, including the argument
property encodings and the root index; the server sends that blob per version
rather than encoding thirty argument types at runtime. Because the client
resolves the tree itself, name autocompletion of the entire command set works.

Two things about that encoding are easy to get wrong and cost this session both:
a node's redirect comes *before* its name, and the packet ends with the root's
index *after* the nodes. Either one leaves the client walking off the end of the
tree, which takes the connection down.

The third, and the one that actually took it down, is the argument properties: a
`brigadier:double` bound is eight bytes where a `float`'s is four, and a
`brigadier:string` carries the enum's ordinal (`word`/`phrase`/`greedy`) rather
than the names the report uses. A single bound written four bytes short
desynchronises every node after it, which a client reports as "failed to decode
packet 'clientbound/minecraft:commands'". `TestCommandTreeIsGenerated` decodes
the generated body the way a client does, so a drift shows up as trailing bytes
instead of as a disconnect.

Dispatch is still by string, for the handful of commands this server runs
(`help`, `list`, `spawn`, `time`, `weather`, `gamemode`, `difficulty`, `tp`,
`setblock`, `summon`, `scoreboard`, `bossbar`, `gamerule`), with replies as
system chat;
anything else is answered "Unknown command", so a client that offers a command
from the tree and sends it gets a reply rather than silence.

**Missing**: argument *suggestions* (the Suggestions packet), permissions, and
the execution of the rest of the tree.

## Sounds, particles and effects

**Implemented (sounds)**: block break, place and step sounds; container open and
close; and **mob noise** — a mob's ambient, hurt and death sounds go out bound to
the mob (`sound_entity`), so the client keeps them with the entity as it moves. A
container's creak and its lid animation stay separate packets, as in vanilla.

**Missing**: block breaking and placing sounds are the client's own prediction
and break particles come from the block update, so those were never ours to send
— but **particles of our own** are: nothing here emits `level_particles`, so an
explosion's smoke is the client's doing alone, and ambient effects are absent.

## What full parity would take

Measured against the decompiled 26.3 server: **5027 source files**, of which
`world` alone is 2715. This server is **12227 lines** of hand-written Go plus
about 114000 lines generated from the game's own data — packet tables,
registries, recipes, block states, the command tree, the light tables. It
implements the **protocol and the plumbing** and a slice of the simulation; what
remains is the *breadth* of the simulation.

| subsystem | vanilla | here |
|---|---|---|
| protocol: login, config, play, chunk streaming, light payloads | ~416 files | the packets this server uses |
| block behaviour | 330 block classes | placement, use, about a dozen interactive blocks |
| items | ~98 item classes, 53 component classes | stacks and components carried; a handful interpreted |
| entities | ~85 top-level plus subpackages | mobs, TNT, dropped items; no projectiles, vehicles or AI |
| redstone | inside the block classes | absent |
| recipes | 56 classes | crafting and furnace cooking |
| menus | 61 classes | chest, furnace, crafting, anvil, shulker, hopper, lectern |
| commands | 124 classes | 13 dispatched by string |
| advancements | 115 classes | absent |
| worldgen | 41 classes plus data | a saved world, or the flat one |
| datafixers | 282 classes | absent: an old world's NBT is not migrated |
| block and item ticks | the block classes | fluids and furnaces only |

The gaps that matter most for *playing*: **mob AI** (no pathfinding, no goals),
**redstone**, **worldgen**, hunger, experience, potions and effects, enchanting,
brewing, trading and **advancements** — and the nether and the end.

## Data pipeline

Everything version-specific is generated from the vanilla jar and its data
generator reports: packet ids, registries, tags, block states, menus, recipes,
fuel, item names, block entities, placement data, emission and opacity. The
tables that vanilla keeps in *code* rather than data — emission, opacity, sound
groups, hardness — are written out by hand and marked as approximations here.
