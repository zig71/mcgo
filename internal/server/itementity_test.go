package server

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// A dropped item carries its stack in entity metadata. Without that entry the
// client has an entity to draw and nothing to draw in it.
func TestDroppedItemMetadata(t *testing.T) {
	s := &session{t: v777.Tables}
	itemID := itemOf(t, "minecraft:diamond")
	body := s.entityMetadataBody(7, metaItemStack, serializerItemStack, func(w *mcproto.Writer) {
		s.writeSlot(w, world.Item{ID: itemID, Count: 3})
	})

	// The stack on the wire: the count, the item id as a varint, and the two
	// component counts, which are zero for a plain stack.
	var stack mcproto.Writer
	stack.WriteVarInt(3)
	stack.WriteVarInt(itemID)
	stack.WriteByte(0) // no components added
	stack.WriteByte(0) // none removed
	want := append([]byte{
		0x07,                      // entity id
		byte(metaItemStack),       // metadata index 8
		byte(serializerItemStack), // serializer 7, ITEM_STACK
	}, stack.Bytes()...)
	want = append(want, metaTerminator)
	if !bytes.Equal(body, want) {
		t.Fatalf("item metadata\n got %x\nwant %x", body, want)
	}
}

// A drop lands next to one of the same item and merges into it rather than
// standing beside it.
func TestDroppedItemsMerge(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	stone := world.Item{ID: itemOf(t, "minecraft:stone"), Count: 20}

	first := srv.spawnItemEntity(tab, stone, 0.5, 65, 0.5)
	if first == nil {
		t.Fatal("the first drop did not spawn")
	}
	second := srv.spawnItemEntity(tab, world.Item{ID: stone.ID, Count: 10}, 0.7, 65, 0.5)

	if second != first {
		t.Fatal("the second drop did not merge into the first")
	}
	if first.Item.Count != 30 {
		t.Errorf("the merged stack holds %d, want 30", first.Item.Count)
	}
	if n := len(srv.entities()); n != 1 {
		t.Errorf("%d entities after merging, want 1", n)
	}

	// A different item does not merge.
	other := srv.spawnItemEntity(tab, world.Item{ID: itemOf(t, "minecraft:dirt"), Count: 1}, 0.7, 65, 0.5)
	if other == first {
		t.Error("a different item merged into the stack")
	}
}

// Taking fills matching stacks before empty slots, and reports what it took.
func TestTakeItemFillsStacks(t *testing.T) {
	s := placer(0, 0)
	stone := itemOf(t, "minecraft:stone")
	s.inv[0] = world.Item{ID: stone, Count: 60}
	s.inv[5] = world.Item{ID: itemOf(t, "minecraft:dirt"), Count: 1}

	stack := world.Item{ID: stone, Count: 8}
	if !s.takeItem(&stack) {
		t.Fatal("taking the item changed nothing")
	}
	if stack.Count != 0 {
		t.Errorf("the leftover is %d, want none", stack.Count)
	}
	// Four went into the matching stack, the other four into the first empty
	// slot, as Inventory.add does.
	if s.inv[0].Count != 64 {
		t.Errorf("the matching stack holds %d, want 64", s.inv[0].Count)
	}
	if got := s.inv[1]; got.ID != stone || got.Count != 4 {
		t.Errorf("the remainder landed in %+v, want four stone in the first empty slot", got)
	}
	if got := s.inv[3]; !got.Empty() {
		t.Errorf("a further slot was used: %+v", got)
	}
	// No room means nothing is taken.
	full := world.Item{ID: itemOf(t, "minecraft:bedrock"), Count: 1}
	for i := range s.inv {
		s.inv[i] = world.Item{ID: itemOf(t, "minecraft:dirt"), Count: 64}
	}
	if s.hasRoomFor(full) {
		t.Error("a full inventory reported room")
	}
	if s.takeItem(&full) {
		t.Error("a full inventory took an item anyway")
	}
}

// Items do not last forever.
func TestDroppedItemsDespawn(t *testing.T) {
	srv := fluidServer()
	e := srv.spawnItemEntity(v777.Tables, world.Item{ID: itemOf(t, "minecraft:stone"), Count: 1}, 0.5, 65, 0.5)
	if e == nil {
		t.Fatal("the drop did not spawn")
	}
	e.Age = itemDespawnTicks
	srv.tickEntities()
	if n := len(srv.entities()); n != 0 {
		t.Errorf("%d entities after the despawn age, want none", n)
	}
}

// An explosion leaves some of what it broke behind as items.
func TestExplosionDropsItems(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	for i := int32(0); i < 40; i++ {
		w.SetBlock(2+i%8, 65, 2+i/8, stone)
	}

	srv.explode(tab, w, 5.5, 65, 5.5, tntPower)

	if n := len(srv.entities()); n == 0 {
		t.Fatal("the explosion dropped nothing")
	}
	if n := len(srv.entities()); n > maxExplosionDrops {
		t.Errorf("%d drops, want at most %d", n, maxExplosionDrops)
	}
	for _, e := range srv.entities() {
		if e.Kind != kindItem || e.Item.ID == 0 || e.Item.Count == 0 {
			t.Errorf("a drop with no stack in it: %+v", e)
		}
	}
}

// dumpPackets names packets by their id and contents for a failure message.
func dumpPackets(packets []mcproto.Packet) string {
	var b bytes.Buffer
	for _, p := range packets {
		fmt.Fprintf(&b, "id=%d data=%x; ", p.ID, p.Data)
	}
	return b.String()
}

// A player standing on a drop picks it up.
func TestPlayerPicksUpADrop(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	stone := itemOf(t, "minecraft:stone")

	sess := placer(0, 0)
	sess.srv = srv
	sess.entityID = 11
	sess.posX, sess.posY, sess.posZ = 0.5, 65, 0.5
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()
	sess.c = mcproto.NewConn(serverEnd)

	// Read what the server sends, so its writes do not block.
	got := make(chan mcproto.Packet, 16)
	go func() {
		c := mcproto.NewConn(clientEnd)
		for {
			p, err := c.ReadPacket()
			if err != nil {
				return
			}
			got <- *p
		}
	}()

	e := srv.spawnItemEntity(tab, world.Item{ID: stone, Count: 5}, 0.6, 65, 0.5)
	if e == nil {
		t.Fatal("the drop did not spawn")
	}
	sessionRegisterForTest(srv, sess)
	srv.tickEntities()

	if n := len(srv.entities()); n != 0 {
		t.Errorf("%d entities after the pickup, want none", n)
	}
	total := int32(0)
	for _, it := range sess.inv {
		if it.ID == stone {
			total += it.Count
		}
	}
	if total != 5 {
		t.Errorf("the player holds %d stone, want 5", total)
	}

	// The client is told to show the item flying into the player, and is sent
	// the inventory it landed in; without the second the client's inventory
	// disagrees with the server's.
	//
	// The packets are identified by their contents rather than their ids, so
	// the test says which packet it means and not which number it happens to
	// have in one protocol version.
	var packets []mcproto.Packet
	for i := 0; i < 3; i++ {
		select {
		case p := <-got:
			packets = append(packets, p)
		case <-time.After(2 * time.Second):
			t.Fatalf("only got %d packets: %s", len(packets), dumpPackets(packets))
		}
	}

	// Take Item: the drop's id, the player's id, the amount. The ids come from
	// the store, so they are read here rather than written out.
	var take mcproto.Writer
	take.WriteVarInt(e.ID)
	take.WriteVarInt(sess.entityID)
	take.WriteVarInt(5)
	sawTake := false
	for _, p := range packets {
		if bytes.Equal(p.Data, take.Bytes()) {
			sawTake = true
		}
	}
	if !sawTake {
		t.Errorf("no Take Item packet: %s", dumpPackets(packets))
	}

	// Container Set Content: window 0, then the 46 slots the player sends.
	sawContent := false
	for _, p := range packets {
		if len(p.Data) > 3 && p.Data[0] == 0 && p.Data[1] == 0 && p.Data[2] == 46 {
			sawContent = true
		}
	}
	if !sawContent {
		t.Errorf("no Container Set Content packet: %s", dumpPackets(packets))
	}
}
