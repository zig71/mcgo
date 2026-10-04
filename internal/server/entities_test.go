package server

import (
	"testing"

	"mcgo/internal/proto/v777"
)

// A spawn egg names the mob it holds.
func TestSpawnEggNames(t *testing.T) {
	for item, want := range map[string]string{
		"minecraft:zombie_spawn_egg": "minecraft:zombie",
		"minecraft:cow_spawn_egg":    "minecraft:cow",
		"minecraft:oak_boat":         "",
		"minecraft:spawn_egg":        "",
	} {
		got, ok := spawnEggEntity(item)
		if want == "" {
			if ok {
				t.Errorf("%s was taken for a spawn egg (%s)", item, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%s holds %q (ok=%v), want %q", item, got, ok, want)
		}
	}
}

// The mob a spawn egg names has to be found in the entity registry. Parsing the
// name is not enough: the lookup key must be namespaced exactly once, and a
// doubled namespace is what a hand-written lookup gets wrong.
func TestSpawnEggFindsAnEntityType(t *testing.T) {
	for _, item := range []string{
		"minecraft:zombie_spawn_egg", "minecraft:cow_spawn_egg", "minecraft:creeper_spawn_egg",
	} {
		mob, ok := spawnEggEntity(item)
		if !ok {
			t.Fatalf("%s is not recognised as a spawn egg", item)
		}
		if _, ok := v777.Tables.EntityIDs[mob]; !ok {
			t.Errorf("%q is not an entity type (from %s)", mob, item)
		}
	}
}

// Entities fall and land on the ground rather than sinking through it.
func TestEntityFallsAndLands(t *testing.T) {
	srv := fluidServer()
	e := srv.spawnEntity(kindMob, 1, 0.5, 70, 0.5)

	for range 200 {
		srv.tickEntities()
	}

	if e.Y != 65 {
		t.Errorf("the entity rests at y=%.2f, want 65 (the flat world's surface)", e.Y)
	}
	if !e.OnGround {
		t.Error("the entity is not on the ground")
	}
}

// Primed TNT burns down and then removes the blocks around it.
func TestTNTEExplodes(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	w.SetBlock(2, 65, 0, stone)

	e := srv.spawnEntity(kindTNT, 1, 0.5, 65, 0.5)
	e.Fuse = 1
	srv.tickEntities()

	if got := w.Block(2, 65, 0); got != tab.BlockAir {
		t.Errorf("the block beside the TNT survived the explosion: %d", got)
	}
	// The TNT is gone. What is left is what it blew up: items lying on the
	// ground, which the blast leaves behind.
	for _, e := range srv.entities() {
		if e.Kind == kindTNT {
			t.Error("the primed TNT survived its own explosion")
		}
		if e.Kind != kindItem {
			t.Errorf("an entity of kind %d survived the explosion", e.Kind)
		}
	}
}

// Spawning and removing keep the store honest, and each entity gets its own id.
func TestEntityLifecycle(t *testing.T) {
	srv := fluidServer()
	a := srv.spawnEntity(kindMob, 1, 0.5, 65, 0.5)
	b := srv.spawnEntity(kindMob, 1, 1.5, 65, 0.5)

	if a.ID == b.ID {
		t.Fatalf("two entities share id %d", a.ID)
	}
	if a.uuid == b.uuid {
		t.Error("two entities share a uuid")
	}
	if len(srv.entities()) != 2 {
		t.Fatalf("%d entities, want 2", len(srv.entities()))
	}

	srv.removeEntity(a.ID)
	if n := len(srv.entities()); n != 1 {
		t.Errorf("%d entities after removing one, want 1", n)
	}
	// Removing something that is gone is not an error.
	srv.removeEntity(a.ID)
}
