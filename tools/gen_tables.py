#!/usr/bin/env python3
"""Emit a version's proto.Tables table (tables_gen.go).

Usage: gen_tables.py <reports_dir> <server_jar> <out_dir>

Separate from gen_protocol.py so the version-table contract can evolve without
touching the bulk generators. Reads packets.json (IDs), version.json (protocol
number + version string) and references the sibling generated vars for world
data.
"""
import json
import sys
import zipfile
from pathlib import Path

MODULE = "mcgo"

# Go field name -> (state, direction, packet name)
FIELDS = [
    ("LoginServerboundHello", "login", "serverbound", "hello"),
    ("LoginServerboundLoginAcknowledged", "login", "serverbound", "login_acknowledged"),
    ("LoginClientboundLoginFinished", "login", "clientbound", "login_finished"),
    ("ConfigServerboundClientInformation", "configuration", "serverbound", "client_information"),
    ("ConfigServerboundCookieResponse", "configuration", "serverbound", "cookie_response"),
    ("ConfigServerboundCustomPayload", "configuration", "serverbound", "custom_payload"),
    ("ConfigServerboundKeepAlive", "configuration", "serverbound", "keep_alive"),
    ("ConfigServerboundPong", "configuration", "serverbound", "pong"),
    ("ConfigServerboundSelectKnownPacks", "configuration", "serverbound", "select_known_packs"),
    ("ConfigServerboundFinishConfiguration", "configuration", "serverbound", "finish_configuration"),
    ("ConfigClientboundUpdateEnabledFeatures", "configuration", "clientbound", "update_enabled_features"),
    ("ConfigClientboundSelectKnownPacks", "configuration", "clientbound", "select_known_packs"),
    ("ConfigClientboundRegistryData", "configuration", "clientbound", "registry_data"),
    ("ConfigClientboundUpdateTags", "configuration", "clientbound", "update_tags"),
    ("ConfigClientboundFinishConfiguration", "configuration", "clientbound", "finish_configuration"),
    ("PlayClientboundLogin", "play", "clientbound", "login"),
    ("PlayClientboundGameEvent", "play", "clientbound", "game_event"),
    ("PlayClientboundLevelChunkWithLight", "play", "clientbound", "level_chunk_with_light"),
    ("PlayClientboundSetChunkCacheCenter", "play", "clientbound", "set_chunk_cache_center"),
    ("PlayClientboundForgetLevelChunk", "play", "clientbound", "forget_level_chunk"),
    ("PlayClientboundSetDefaultSpawnPosition", "play", "clientbound", "set_default_spawn_position"),
    ("PlayClientboundSetSimulationDistance", "play", "clientbound", "set_simulation_distance"),
    ("PlayClientboundPlayerPosition", "play", "clientbound", "player_position"),
    ("PlayClientboundPlayerAbilities", "play", "clientbound", "player_abilities"),
    ("PlayClientboundKeepAlive", "play", "clientbound", "keep_alive"),
    ("PlayClientboundSystemChat", "play", "clientbound", "system_chat"),
    ("PlayClientboundContainerSetContent", "play", "clientbound", "container_set_content"),
    ("PlayClientboundContainerSetData", "play", "clientbound", "container_set_data"),
    ("PlayClientboundSetHeldSlot", "play", "clientbound", "set_held_slot"),
    ("PlayClientboundBlockEntityData", "play", "clientbound", "block_entity_data"),
    ("PlayClientboundOpenSignEditor", "play", "clientbound", "open_sign_editor"),
    ("PlayClientboundOpenScreen", "play", "clientbound", "open_screen"),
    ("PlayClientboundContainerClose", "play", "clientbound", "container_close"),
    ("PlayClientboundPlayerInfoUpdate", "play", "clientbound", "player_info_update"),
    ("PlayClientboundPlayerInfoRemove", "play", "clientbound", "player_info_remove"),
    ("PlayClientboundTabList", "play", "clientbound", "tab_list"),
    ("PlayClientboundDisguisedChat", "play", "clientbound", "disguised_chat"),
    ("PlayClientboundAddEntity", "play", "clientbound", "add_entity"),
    ("PlayClientboundRemoveEntities", "play", "clientbound", "remove_entities"),
    ("PlayClientboundMoveEntityPosRot", "play", "clientbound", "move_entity_pos_rot"),
    ("PlayClientboundRotateHead", "play", "clientbound", "rotate_head"),
    ("PlayServerboundAcceptTeleportation", "play", "serverbound", "accept_teleportation"),
    ("PlayServerboundKeepAlive", "play", "serverbound", "keep_alive"),
    ("PlayServerboundMovePlayerPos", "play", "serverbound", "move_player_pos"),
    ("PlayServerboundMovePlayerPosRot", "play", "serverbound", "move_player_pos_rot"),
    ("PlayServerboundMovePlayerRot", "play", "serverbound", "move_player_rot"),
    ("PlayServerboundMovePlayerStatusOnly", "play", "serverbound", "move_player_status_only"),
    ("PlayServerboundChat", "play", "serverbound", "chat"),
    ("PlayServerboundSignUpdate", "play", "serverbound", "sign_update"),
    ("PlayServerboundContainerClick", "play", "serverbound", "container_click"),
    ("PlayServerboundContainerClose", "play", "serverbound", "container_close"),
    ("PlayServerboundContainerButtonClick", "play", "serverbound", "container_button_click"),
    ("PlayClientboundBlockChangedAck", "play", "clientbound", "block_changed_ack"),
    ("PlayClientboundBlockUpdate", "play", "clientbound", "block_update"),
    ("PlayClientboundBlockEvent", "play", "clientbound", "block_event"),
    ("PlayServerboundPlayerAction", "play", "serverbound", "player_action"),
    ("PlayServerboundUseItemOn", "play", "serverbound", "use_item_on"),
    ("PlayServerboundUseItem", "play", "serverbound", "use_item"),
    ("PlayClientboundLightUpdate", "play", "clientbound", "light_update"),
    ("PlayClientboundSetTime", "play", "clientbound", "set_time"),
    ("PlayServerboundSetCarriedItem", "play", "serverbound", "set_carried_item"),
    ("PlayServerboundBlockEntityTagQuery", "play", "serverbound", "block_entity_tag_query"),
    ("PlayClientboundChunkBatchStart", "play", "clientbound", "chunk_batch_start"),
    ("PlayClientboundChunkBatchFinished", "play", "clientbound", "chunk_batch_finished"),
    ("PlayServerboundChunkBatchReceived", "play", "serverbound", "chunk_batch_received"),
    ("PlayClientboundTagQuery", "play", "clientbound", "tag_query"),
    ("PlayServerboundSetCreativeModeSlot", "play", "serverbound", "set_creative_mode_slot"),
    ("PlayServerboundRenameItem", "play", "serverbound", "rename_item"),
    ("PlayServerboundChatCommand", "play", "serverbound", "chat_command"),
    ("PlayServerboundChatCommandSigned", "play", "serverbound", "chat_command_signed"),
    ("PlayClientboundCommands", "play", "clientbound", "commands"),
    ("PlayClientboundChangeDifficulty", "play", "clientbound", "change_difficulty"),
    ("PlayClientboundSetEquipment", "play", "clientbound", "set_equipment"),
    ("PlayClientboundSwingAnimation", "play", "clientbound", "swing_animation"),
    ("PlayClientboundAnimate", "play", "clientbound", "animate"),
    ("PlayServerboundPunch", "play", "serverbound", "punch"),
    ("PlayServerboundPlayerInput", "play", "serverbound", "player_input"),
    ("PlayServerboundPickItemFromBlock", "play", "serverbound", "pick_item_from_block"),
    ("PlayClientboundContainerSetSlot", "play", "clientbound", "container_set_slot"),
    ("PlayServerboundPlayerCommand", "play", "serverbound", "player_command"),
    ("PlayClientboundSetEntityData", "play", "clientbound", "set_entity_data"),
    ("PlayClientboundLevelEvent", "play", "clientbound", "level_event"),
    ("PlayClientboundSetHealth", "play", "clientbound", "set_health"),
    ("PlayClientboundDamageEvent", "play", "clientbound", "damage_event"),
    ("PlayClientboundEntityEvent", "play", "clientbound", "entity_event"),
    ("PlayClientboundExplode", "play", "clientbound", "explode"),
    ("PlayClientboundUpdateRecipes", "play", "clientbound", "update_recipes"),
    ("PlayClientboundRecipeBookAdd", "play", "clientbound", "recipe_book_add"),
    ("PlayClientboundRecipeBookSettings", "play", "clientbound", "recipe_book_settings"),
    ("PlayClientboundSoundEffect", "play", "clientbound", "sound"),
    ("PlayClientboundSoundEntity", "play", "clientbound", "sound_entity"),
    ("PlayClientboundSetEntityMotion", "play", "clientbound", "set_entity_motion"),
    ("PlayClientboundBlockDestruction", "play", "clientbound", "block_destruction"),
    ("PlayClientboundSetObjective", "play", "clientbound", "set_objective"),
    ("PlayClientboundSetScore", "play", "clientbound", "set_score"),
    ("PlayClientboundSetDisplayObjective", "play", "clientbound", "set_display_objective"),
    ("PlayClientboundBossEvent", "play", "clientbound", "boss_event"),
    ("PlayClientboundAwardStatistics", "play", "clientbound", "award_stats"),
    ("PlayClientboundResetScore", "play", "clientbound", "reset_score"),
    ("PlayClientboundHurtAnimation", "play", "clientbound", "hurt_animation"),
    ("PlayClientboundRespawn", "play", "clientbound", "respawn"),
    ("PlayServerboundClientCommand", "play", "serverbound", "client_command"),
    ("PlayServerboundInteract", "play", "serverbound", "interact"),
    ("PlayServerboundAttack", "play", "serverbound", "attack"),
    ("PlayServerboundChatAck", "play", "serverbound", "chat_ack"),
    ("PlayServerboundChatSessionUpdate", "play", "serverbound", "chat_session_update"),
    ("PlayClientboundPlayerCombatKill", "play", "clientbound", "player_combat_kill"),
    ("PlayClientboundTakeItemEntity", "play", "clientbound", "take_item_entity"),
]

# Protocol thresholds for shape changes.
SESSION_ID_SINCE = 776   # Session ID UUID appended to Login Success
ONLINE_MODE_SINCE = 776  # Online mode boolean added to Login (play)


def main() -> None:
    reports, jar, outdir = Path(sys.argv[1]), sys.argv[2], Path(sys.argv[3])
    packets = json.loads((reports / "packets.json").read_text())
    vj = json.loads(zipfile.ZipFile(jar).read("version.json"))
    protocol, version = vj["protocol_version"], vj["id"]

    def pid(state: str, direction: str, packet: str) -> int:
        # 0 means this version has no such packet. Packets are added and
        # removed between versions (swing_animation is new in 26.3), and a
        # missing one must not abort the whole table.
        entry = packets[state][direction].get(f"minecraft:{packet}")
        return entry["protocol_id"] if entry else 0

    out = [
        "// Code generated by tools/gen_tables.py. DO NOT EDIT.",
        "",
        f"package {outdir.name}",
        "",
        f'import "{MODULE}/internal/proto"',
        "",
        "// Tables is the wire table for this protocol version.",
        "var Tables = proto.Tables{",
        f"\tProtocol:    {protocol},",
        f'\tVersionName: "{version}",',
        f'\tKnownPack:   "{version}",',
        "",
    ]
    for go_name, state, direction, packet in FIELDS:
        out.append(f"\t{go_name}: {pid(state, direction, packet)},")
    out += [
        "",
        f"\tLoginSuccessHasSessionID: {str(protocol >= SESSION_ID_SINCE).lower()},",
        f"\tPlayLoginHasOnlineMode:   {str(protocol >= ONLINE_MODE_SINCE).lower()},",
        "",
        "\tBlockAir:     BlockAir,",
        "\tBlockBedrock: BlockBedrock,",
        "\tBlockDirt:    BlockDirt,",
        "\tBlockGrass:   BlockGrassBlock,",
        "\tBlockStone:   BlockStone,",
        "",
        "\tRegistryEntries: RegistryEntries,",
        "\tRegistryOrder: RegistryOrder,",
        "\tRegistryTags:    RegistryTags,",
        "\tRegistryNBT:     RegistryNBT,",
        "\tItemIDs:        ItemIDs,",
        "\tCustomNameComponent: CustomNameComponent,",
        "\tBannerPatternsComponent: BannerPatternsComponent,",
        "\tItemBlockName:  ItemBlockName,",
        "\tBlockProps:     BlockProps,",
        "\tBlockItems:     BlockItems,",
        "\tWallVariant:    WallVariant,",
        "\tSixWayFacing:   SixWayFacing,",
        "\tItemBlockState:  ItemBlockState,",
        "\tSignBlockStates: SignBlockStates,",
        "\tSignBlockEntityType: SignBlockEntityTypeID,",
        "\tBlockStates: BlockStates,",
        "\tLightDampening: LightDampening,",
        "\tLightEmission: LightEmission,",
        "\tBreakHardness: BreakHardness,",
        "\tBlockTools: BlockTools,",
        "\tCommandsBytes: CommandsBytes,",
        "\tItemName:           ItemName,",
        "\tEntityIDs:          EntityIDs,",
        "\tSoundEventIDs:      SoundEventIDs,",
        "\tParticleTypeIDs:    ParticleTypeIDs,",
        "\tRecipeDisplayIDs:   RecipeDisplayIDs,",
        "\tSlotDisplayIDs:     SlotDisplayIDs,",
        "\tRecipeBookCategoryIDs: RecipeBookCategoryIDs,",
        "\tCommandArgumentTypeIDs: CommandArgumentTypeIDs,",
        "\tStatTypeIDs:            StatTypeIDs,",
        "\tCustomStatIDs:          CustomStatIDs,",
        "\tBlockIDs:               BlockIDs,",
        "\tBlockEmission:      BlockEmission,",
        "\tLightOpaque:        LightOpaque,",
        "\tBlockEntityForState: BlockEntityForState,",
        "\tBlockEntityTypes: BlockEntityTypes,",
        "\tContainerMenus: ContainerMenus,",
        "\tFurnaceLitOn:  FurnaceLitOn,",
        "\tFurnaceLitOff: FurnaceLitOff,",
        "\tFurnaceRecipes: FurnaceRecipes,",
        "\tBlastRecipes:   BlastRecipes,",
        "\tSmokerRecipes:  SmokerRecipes,",
        "\tFuel:           Fuel,",
        "\tCraftRemainder: CraftRemainder,",
        "\tCraftingIngredients: CraftingIngredients,",
        "\tCraftingRecipes:     CraftingRecipes,",
        "\tPlayerEntityType: PlayerEntityTypeID,",
        "}",
    ]
    outdir.mkdir(parents=True, exist_ok=True)
    (outdir / "tables_gen.go").write_text("\n".join(out))
    print(f"wrote {outdir}/tables_gen.go (protocol {protocol}, {version})")


if __name__ == "__main__":
    main()
