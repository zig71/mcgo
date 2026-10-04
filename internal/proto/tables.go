// Package proto defines the version-independent contract between the session
// core and a version-specific wire table. Each proto/vXXX package provides one
// Tables value; the server picks one from the client's handshake protocol
// number, so the session core never references a version directly.
package proto

// MenuSpec is one container menu: its ID in the menu registry, how many slots
// its container half has, and the translation key of its title. Vanilla picks
// the title per block, so a chest and a barrel share the menu ID but not the
// title.
type MenuSpec struct {
	ID    int32
	Slots int32
	Title string
}

// CookingRecipe is one furnace-family recipe: what comes out, how many, and
// the base cooking time in ticks.
type CookingRecipe struct {
	Result int32
	Count  int32
	Time   int32
}

// CraftingRecipe is one shaped or shapeless crafting recipe. A shaped recipe
// lists its grid row by row; a slot is an index+1 into CraftingIngredients, or
// 0 for a cell that must be empty. A shapeless recipe lists only the
// ingredients it needs, in any arrangement.
type CraftingRecipe struct {
	Shapeless bool
	Width     int32
	Height    int32
	Slots     []int32
	Result    int32
	Count     int32
}

// Tables is every version-specific wire fact the server needs.
type Tables struct {
	Protocol    int32
	VersionName string
	KnownPack   string // version string advertised for minecraft:core

	// Login state.
	LoginServerboundHello             int32
	LoginServerboundLoginAcknowledged int32
	LoginClientboundLoginFinished     int32
	LoginSuccessHasSessionID          bool // added in 26.2 (776)

	// Configuration state.
	ConfigServerboundClientInformation   int32
	ConfigServerboundCookieResponse      int32
	ConfigServerboundCustomPayload       int32
	ConfigServerboundKeepAlive           int32
	ConfigServerboundPong                int32
	ConfigServerboundSelectKnownPacks    int32
	ConfigServerboundFinishConfiguration int32

	ConfigClientboundUpdateEnabledFeatures int32
	ConfigClientboundSelectKnownPacks      int32
	ConfigClientboundRegistryData          int32
	ConfigClientboundUpdateTags            int32
	ConfigClientboundFinishConfiguration   int32

	// Play state.
	PlayClientboundLogin                   int32
	PlayClientboundGameEvent               int32
	PlayClientboundLevelChunkWithLight     int32
	PlayClientboundSetChunkCacheCenter     int32
	PlayClientboundForgetLevelChunk        int32
	PlayClientboundSetDefaultSpawnPosition int32
	PlayClientboundSetSimulationDistance   int32
	PlayClientboundPlayerPosition          int32
	PlayClientboundPlayerAbilities         int32
	PlayClientboundKeepAlive               int32
	PlayClientboundSystemChat              int32
	PlayClientboundContainerSetContent     int32
	PlayClientboundContainerSetData        int32
	PlayClientboundSetHeldSlot             int32
	PlayClientboundBlockEntityData         int32
	PlayServerboundBlockEntityTagQuery     int32
	PlayClientboundChunkBatchStart         int32
	PlayClientboundChunkBatchFinished      int32
	PlayServerboundChunkBatchReceived      int32
	PlayClientboundTagQuery                int32
	PlayClientboundOpenSignEditor          int32
	PlayClientboundOpenScreen              int32
	PlayClientboundContainerClose          int32
	PlayClientboundPlayerInfoUpdate        int32
	PlayClientboundPlayerInfoRemove        int32
	PlayClientboundTabList                 int32
	PlayClientboundDisguisedChat           int32
	PlayClientboundAddEntity               int32
	PlayClientboundRemoveEntities          int32
	PlayClientboundMoveEntityPosRot        int32
	PlayClientboundRotateHead              int32
	PlayerEntityType                       int32
	PlayClientboundBlockChangedAck         int32
	PlayClientboundBlockUpdate             int32
	PlayClientboundBlockEvent              int32
	PlayClientboundLightUpdate             int32
	PlayClientboundSetTime                 int32

	PlayServerboundAcceptTeleportation  int32
	PlayServerboundKeepAlive            int32
	PlayServerboundMovePlayerPos        int32
	PlayServerboundMovePlayerPosRot     int32
	PlayServerboundMovePlayerRot        int32
	PlayServerboundMovePlayerStatusOnly int32
	PlayServerboundChat                 int32
	PlayServerboundSignUpdate           int32
	PlayServerboundContainerClick       int32
	PlayServerboundContainerClose       int32
	PlayServerboundContainerButtonClick int32
	PlayServerboundPlayerAction         int32
	PlayServerboundUseItemOn            int32
	PlayServerboundUseItem              int32
	PlayServerboundSetCarriedItem       int32
	PlayServerboundSetCreativeModeSlot  int32
	PlayServerboundRenameItem           int32
	PlayServerboundChatCommand          int32
	PlayServerboundChatCommandSigned    int32
	PlayClientboundCommands             int32
	PlayClientboundChangeDifficulty     int32
	PlayClientboundSetEquipment         int32
	PlayClientboundSwingAnimation       int32
	PlayClientboundAnimate              int32
	PlayServerboundPunch                int32
	PlayServerboundPlayerInput          int32
	PlayServerboundPickItemFromBlock    int32
	PlayClientboundContainerSetSlot     int32
	// BlockItems maps a block name to the item that places it.
	BlockItems                         map[string]int32
	PlayServerboundPlayerCommand       int32
	PlayClientboundSetEntityData       int32
	PlayClientboundLevelEvent          int32
	PlayClientboundSetHealth           int32
	PlayClientboundDamageEvent         int32
	PlayClientboundEntityEvent         int32
	PlayClientboundLevelParticles      int32
	PlayClientboundExplode             int32
	PlayClientboundUpdateRecipes       int32
	PlayClientboundRecipeBookAdd       int32
	PlayClientboundRecipeBookSettings  int32
	PlayClientboundSoundEffect         int32
	PlayClientboundSoundEntity         int32
	PlayClientboundSetEntityMotion     int32
	PlayClientboundBlockDestruction    int32
	PlayClientboundSetObjective        int32
	PlayClientboundSetScore            int32
	PlayClientboundSetDisplayObjective int32
	PlayClientboundBossEvent           int32
	PlayClientboundAwardStatistics     int32
	PlayClientboundResetScore          int32
	PlayClientboundHurtAnimation       int32
	PlayClientboundRespawn             int32
	PlayServerboundClientCommand       int32
	PlayServerboundInteract            int32
	PlayServerboundAttack              int32
	PlayServerboundChatAck             int32
	PlayServerboundChatSessionUpdate   int32
	PlayClientboundPlayerCombatKill    int32
	PlayClientboundTakeItemEntity      int32

	// ItemIDs maps a minecraft:item name to its registry ID. minecraft:item
	// is a built-in registry, so it is not in RegistryEntries.
	ItemIDs map[string]int32
	// CustomNameComponent is minecraft:custom_name in the data component type
	// registry: the component a renamed item carries.
	CustomNameComponent int32
	// BannerPatternsComponent is minecraft:banner_patterns. Its value is
	// BannerPatternLayers, the same shape the block entity's patterns field
	// takes, so it is passed through rather than re-encoded.
	BannerPatternsComponent int32
	// ItemBlockName maps a minecraft:item ID to the block it places, and
	// BlockProps to that block's property names.
	ItemBlockName map[int32]string
	BlockProps    map[string][]string
	// WallVariant maps a block to its wall-mounted twin, which is what a sign,
	// banner or torch becomes when placed against a vertical face.
	WallVariant map[string]string
	// SixWayFacing marks blocks whose facing property includes up and down.
	SixWayFacing map[string]bool
	// ItemBlockState maps a minecraft:item ID to the block state it places.
	ItemBlockState map[int32]int32

	// SignBlockStates marks the block state IDs that are signs.
	SignBlockStates map[int32]bool
	// SignBlockEntityType is minecraft:sign in the block_entity_type registry.
	SignBlockEntityType int32
	// BlockStates is the generated name+properties -> state id table.
	BlockStates []byte
	// LightDampening is one byte per block state, indexed by state id: the cost
	// of a light step through it, from the game itself. Empty for a version
	// whose jar could not be dumped.
	LightDampening []byte
	// LightEmission is one byte per block state, indexed by state id: the light
	// it gives off, from the game itself.
	LightEmission []byte
	// BreakHardness is two bytes per block state, big-endian: hardness times a
	// hundred, or -1 for a block that can never be broken.
	BreakHardness []byte
	// BlockTools is one byte per block state; bit 6 means the state requires the
	// correct tool to drop anything.
	BlockTools []byte
	// CommandsBytes is the Commands packet body: the whole command tree,
	// generated rather than encoded at runtime.
	CommandsBytes []byte
	// ItemName maps an item id back to its name.
	ItemName map[int32]string
	// SoundEventIDs and ParticleTypeIDs are the built-in registries the client
	// already knows, which packets reference by id.
	SoundEventIDs          map[string]int32
	ParticleTypeIDs        map[string]int32
	RecipeDisplayIDs       map[string]int32
	SlotDisplayIDs         map[string]int32
	RecipeBookCategoryIDs  map[string]int32
	CommandArgumentTypeIDs map[string]int32
	StatTypeIDs            map[string]int32
	CustomStatIDs          map[string]int32
	BlockIDs               map[string]int32
	// EntityIDs maps an entity type name to its registry id.
	EntityIDs map[string]int32
	// BlockEmission is the light each block state gives off, and LightOpaque
	// the states that stop light.
	BlockEmission map[int32]int32
	LightOpaque   map[int32]bool
	// BlockEntityForState maps a block state that has a block entity to that
	// entity's type name.
	BlockEntityForState map[int32]string
	// BlockEntityTypes maps a block entity id to its registry id.
	BlockEntityTypes map[string]int32
	// ContainerMenus maps a container block state id to the menu it opens:
	// the menu-registry ID, the size of its container half, and its title.
	ContainerMenus map[int32]MenuSpec
	// FurnaceLitOn and FurnaceLitOff map a furnace block state id to the same
	// state with its LIT property flipped, so a cooking furnace can look lit.
	FurnaceLitOn  map[int32]int32
	FurnaceLitOff map[int32]int32
	// FurnaceRecipes, BlastRecipes and SmokerRecipes map an input item id to
	// what that machine cooks it into.
	FurnaceRecipes map[int32]CookingRecipe
	BlastRecipes   map[int32]CookingRecipe
	SmokerRecipes  map[int32]CookingRecipe
	// Fuel maps an item id to its burn time in ticks on a normal furnace.
	Fuel map[int32]int32
	// CraftRemainder maps an item id to what is left behind when it is
	// consumed, such as the empty bucket a lava bucket leaves.
	CraftRemainder map[int32]int32
	// CraftingIngredients holds the acceptable-item sets crafting recipes
	// refer to by index+1.
	CraftingIngredients [][]int32
	// CraftingRecipes are the shaped and shapeless recipes from the data pack.
	CraftingRecipes []CraftingRecipe

	// Login (play) shape differences.
	PlayLoginHasOnlineMode bool // added in 26.x

	// World data.
	BlockAir, BlockBedrock, BlockDirt, BlockGrass, BlockStone int32
	RegistryEntries                                           map[string][]string
	// RegistryOrder is the order to send them in: the client assigns IDs by
	// receipt order and tags are resolved against those IDs.
	RegistryOrder []string
	RegistryTags  map[string]map[string][]int32

	// RegistryNBT is a length-prefixed (uint32 BE) NBT blob, one entry per
	// RegistryEntries element in order. Sent when the client does not select
	// minecraft:core, since it then has no local source for registry data.
	RegistryNBT []byte
}

// EntityTypeName is the name of an entity type id, or "" when the id is not a
// registered type. The tables carry the name-to-id direction only, and the
// reversal is a scan: it is called when something is hit, not per tick.
func EntityTypeName(t Tables, id int32) string {
	for name, n := range t.EntityIDs {
		if n == id {
			return name
		}
	}
	return ""
}
