# mcgo — Roadmap / TODO

Companion to `SPEC.md`. Each milestone is a shippable increment with a concrete
acceptance test. Sizes are relative, not estimates: **S** (an afternoon),
**M** (days), **L** (weeks), **XL** (months).

---

## Done

| | Milestone | Acceptance |
|---|---|---|
| M0 | Handshake + status | scripted client gets JSON + pong |
| M1 | Login + configuration | scripted client reaches Play |
| M2 | Join + flat chunk + position | **vanilla client joins and renders** |
| M3 | Keepalive, chat, movement | client stays connected, chat echoes |
| M4 | Block interaction | break/place round-trips ack + update |
| M6 | Tick loop + incremental sky light | block change recomputes and pushes light; clock advances |
| M5 | Persistence | edits survive a restart |
| M7 | Held item drives placement | placing oak planks yields block state 15, not dirt |
| — | Version dispatch | 772 / 776 / 777 selected from handshake |
| — | Generated data pipeline | packets, registries, tags, NBT, blocks from the vanilla jar |

Packet coverage: **35 of 260**.

---

## M5 — Persistence — DONE

`world.Edits` is the shared, persistable edit log (all protocol versions
reference the same one, since they describe one world). Saves atomically to
`world.json` every 2 s when dirty and on SIGINT/SIGTERM; a hard kill loses at
most one tick.

```json
{"version":1,"edits":[{"x":0,"y":64,"z":0,"id":0},{"x":0,"y":65,"z":0,"id":10}]}
```

Not done: the file is a flat list rewritten in full. Fine at thousands of
edits, wrong at millions — move to the Anvil region format with M9.

---

## M6 — Tick loop and lighting updates — DONE

Column-based sky light (15 above the column's highest opaque block, 0 at or
below it) plus a 20 Hz clock. On a block change the affected sections are
recomputed and pushed with `light_update`; a chunk is only re-lit if it has
edits, otherwise the cached light blob is reused.

Still open from this milestone: `section_blocks_update` for batched changes,
`block_destruction` (break animation), and real BFS propagation — the current
model is column-based, so overhangs and caves would light incorrectly.

---

## M7 — Inventory and items — PARTIAL

Done: `set_carried_item` selects the hotbar slot, `set_creative_mode_slot`
records the item, and placement now places **what the player holds** via a
generated item ID -> block state map (1129 placeable items). Only the item ID is
read from the slot; the data components that follow are ignored.

Still open: `container_set_content` / `container_set_slot` so the server owns
the inventory (currently the client is the authority and we just listen), chests
and other containers, and item entities.

---

## M8 — Players and entities (M)

- **Do**: `player_info_update` (tab list), `add_entity`/`remove_entities`,
  entity metadata serialisation, movement broadcast, `set_entity_motion`.
- **Acceptance**: two clients see each other move and appear in the tab list.
- **Note**: this is where multi-player state first needs real synchronisation —
  introduce the snapshot model properly here, not before.

---

## M9 — World generation (XL)

Replaces the flat world. The single largest item; do not start it before M6–M8.

- **Do**: noise/density functions, biome placement, surface rules, features
  (trees, ores), carvers (caves), structures; chunk generation off the tick
  thread with a bounded queue; **Anvil region-file persistence**.
- **Acceptance**: a generated world matching vanilla terrain for a given seed
  (compare block-by-block against the vanilla server's output for the same seed).
- **Risk**: vanilla worldgen is tens of thousands of lines. Scope it to "looks
  like Minecraft" or to "bit-identical for a seed" — the second is a much
  bigger commitment.

---

## M10 — Survival systems (XL)

- **Do**: health/food/XP/damage/death/respawn; mob AI (goal selectors,
  pathfinding), spawning/despawning, loot tables; crafting and recipes;
  block entities (furnace, chest); redstone.
- **Acceptance**: a player can survive a night.
- **Note**: redstone is a simulator in its own right; treat it as its own
  milestone if it matters.

---

## M11 — Multiplayer services (L)

- **Do**: online mode (AES-CFB8 encryption, Mojang `hasJoined` session auth,
  profile properties/skins), compression (currently disabled), whitelist/ban/ops,
  RCON, Query, Server Management Protocol, Brigadier command tree.
- **Acceptance**: a premium account joins with its real skin; `list`/`stop` work
  over RCON.
- **Note**: the command tree is mandatory for a vanilla client to accept the
  play state gracefully — currently we send none.

---

## Continuous

- **Leanness**: sky-light arrays are ~32 KB of each 37.7 KB chunk. Sending only
  changed light, or a smaller view distance, is the obvious next win. Re-measure
  RSS after every milestone; the 8.3 MB baseline is the asset.
- **Version support**: when a new release lands, run the four generators, diff
  the registry list against the data pack, add one map entry. Nothing else.
- **Regression harness**: extend `mcjoin` for every new packet family. It has
  caught every real bug that the scripted path could see; the ones it missed
  (Session ID, BitSet framing, missing registries) needed the real client or
  the client jar.

---

## Known gaps in what is already built

| Gap | Impact |
|---|---|
| Light not recomputed on block change | wrong shading after edits |
| Placement always places dirt | cosmetic |
| No block-break timing | instant break, creative-like |
| World is one flat chunk set, 5×5 | hard void edge; no chunk streaming |
| Compression off | ~2.5× bandwidth on the ~1.2 MB join |
| No command tree | client shows no commands |
| Known-pack negotiation unused | always sends 263 KB of registry NBT |
