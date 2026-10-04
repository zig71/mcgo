#!/usr/bin/env python3
"""Emit network-NBT blobs for synchronized registry entries.

Usage: gen_registry_nbt.py <server_jar> <out_dir>

Reads the vanilla data pack (data/minecraft/<registry>/<entry>.json) and
converts each entry to network NBT (tag id + payload, no root name) using the
rules at https://minecraft.wiki/w/NBT_format#Conversion_from_JSON.

Writes registry_nbt.bin: for each registry (sorted by name) then each entry
(sorted), a big-endian uint32 length followed by the NBT bytes. The server
walks it in the same order as RegistryEntries.
"""
import io
import json
import struct
import sys
import zipfile
from pathlib import Path

SYNC_REGISTRIES = {
    "dimension_type": "minecraft:dimension_type",
    "worldgen/biome": "minecraft:worldgen/biome",
    "damage_type": "minecraft:damage_type",
    "chat_type": "minecraft:chat_type",
    "trim_material": "minecraft:trim_material",
    "trim_pattern": "minecraft:trim_pattern",
    "banner_pattern": "minecraft:banner_pattern",
    "painting_variant": "minecraft:painting_variant",
    "jukebox_song": "minecraft:jukebox_song",
    "instrument": "minecraft:instrument",
    "wolf_variant": "minecraft:wolf_variant",
    "wolf_sound_variant": "minecraft:wolf_sound_variant",
    "cat_variant": "minecraft:cat_variant",
    "cat_sound_variant": "minecraft:cat_sound_variant",
    "chicken_variant": "minecraft:chicken_variant",
    "chicken_sound_variant": "minecraft:chicken_sound_variant",
    "cow_variant": "minecraft:cow_variant",
    "cow_sound_variant": "minecraft:cow_sound_variant",
    "pig_variant": "minecraft:pig_variant",
    "pig_sound_variant": "minecraft:pig_sound_variant",
    "frog_variant": "minecraft:frog_variant",
    "enchantment": "minecraft:enchantment",
    "dialog": "minecraft:dialog",
    "timeline": "minecraft:timeline",
    "world_clock": "minecraft:world_clock",
    "test_environment": "minecraft:test_environment",
    "test_instance": "minecraft:test_instance",
    "sulfur_cube_archetype": "minecraft:sulfur_cube_archetype",
    "zombie_nautilus_variant": "minecraft:zombie_nautilus_variant",
    "block_transformer": "minecraft:block_transformer",
    "decorated_pot_pattern": "minecraft:decorated_pot_pattern",
    "worldgen/block_state_provider": "minecraft:worldgen/block_state_provider",
}

TAG_BYTE, TAG_SHORT, TAG_INT, TAG_LONG = 1, 2, 3, 4
TAG_FLOAT, TAG_DOUBLE, TAG_BYTE_ARRAY, TAG_STRING = 5, 6, 7, 8
TAG_LIST, TAG_COMPOUND, TAG_INT_ARRAY, TAG_LONG_ARRAY = 9, 10, 11, 12


def _string(s: str) -> bytes:
    b = s.encode("utf-8")
    return struct.pack(">H", len(b)) + b


def _number(v) -> bytes:
    """Numeric conversion per the wiki table (byte/short/int/long/float/double)."""
    if isinstance(v, bool):
        return bytes([TAG_BYTE]) + struct.pack(">b", 1 if v else 0)
    if isinstance(v, int):
        if -128 <= v <= 127:
            return bytes([TAG_BYTE]) + struct.pack(">b", v)
        if -32768 <= v <= 32767:
            return bytes([TAG_SHORT]) + struct.pack(">h", v)
        if -(2**31) <= v <= 2**31 - 1:
            return bytes([TAG_INT]) + struct.pack(">i", v)
        if -(2**63) <= v <= 2**63 - 1:
            return bytes([TAG_LONG]) + struct.pack(">q", v)
        return bytes([TAG_DOUBLE]) + struct.pack(">d", float(v))
    f = float(v)
    if f.is_integer() and -128 <= f <= 127:
        return bytes([TAG_BYTE]) + struct.pack(">b", int(f))
    if f.is_integer() and -32768 <= f <= 32767:
        return bytes([TAG_SHORT]) + struct.pack(">h", int(f))
    if f.is_integer() and -(2**31) <= f <= 2**31 - 1:
        return bytes([TAG_INT]) + struct.pack(">i", int(f))
    if f.is_integer() and -(2**63) <= f <= 2**63 - 1:
        return bytes([TAG_LONG]) + struct.pack(">q", int(f))
    if struct.unpack(">f", struct.pack(">f", f))[0] == f:
        return bytes([TAG_FLOAT]) + struct.pack(">f", f)
    return bytes([TAG_DOUBLE]) + struct.pack(">d", f)


def _payload(v) -> bytes:
    if isinstance(v, str):
        return bytes([TAG_STRING]) + _string(v)
    if isinstance(v, bool) or isinstance(v, (int, float)):
        return _number(v)
    if isinstance(v, list):
        if not v:
            return bytes([TAG_LIST]) + bytes([0]) + struct.pack(">i", 0)
        parts = [_payload(x) for x in v]
        kinds = {p[0] for p in parts}
        if len(kinds) == 1:
            kind = kinds.pop()
            return bytes([TAG_LIST]) + bytes([kind]) + struct.pack(">i", len(parts)) + b"".join(p[1:] for p in parts)
        # Mixed numeric element types: widen every element to double so the list
        # stays homogeneous (codecs coerce numbers, but a list must be typed).
        if kinds <= {TAG_BYTE, TAG_SHORT, TAG_INT, TAG_LONG, TAG_FLOAT, TAG_DOUBLE}:
            body = b"".join(struct.pack(">d", float(x)) for x in v)
            return bytes([TAG_LIST]) + bytes([TAG_DOUBLE]) + struct.pack(">i", len(v)) + body
        raise ValueError("mixed-type array cannot be converted to NBT")
    if isinstance(v, dict):
        out = bytearray([TAG_COMPOUND])
        for k, val in v.items():
            out += bytes([_payload(val)[0]]) + _string(k) + _payload(val)[1:]
        out += bytes([0])
        return bytes(out)
    raise ValueError(f"cannot convert {type(v)}")


def main() -> None:
    jar, outdir = sys.argv[1], Path(sys.argv[2])
    z = zipfile.ZipFile(jar)
    inner = next(n for n in z.namelist() if n.startswith("META-INF/versions/") and n.endswith(".jar"))
    iz = zipfile.ZipFile(io.BytesIO(z.read(inner)))

    blob = bytearray()
    total = 0
    for subdir, _regname in sorted(SYNC_REGISTRIES.items(), key=lambda kv: kv[1]):
        prefix = f"data/minecraft/{subdir}/"
        names = sorted(n for n in iz.namelist() if n.startswith(prefix) and n.endswith(".json"))
        for n in names:
            data = json.loads(iz.read(n))
            try:
                nbt = _payload(data)
            except ValueError as e:
                print(f"FAIL {n}: {e}")
                continue
            blob += struct.pack(">I", len(nbt)) + nbt
            total += 1

    outdir.mkdir(parents=True, exist_ok=True)
    (outdir / "registry_nbt.bin").write_bytes(bytes(blob))
    (outdir / "registry_nbt_gen.go").write_text(
        "// Code generated by tools/gen_registry_nbt.py. DO NOT EDIT.\n\n"
        f"package {outdir.name}\n\n"
        'import _ "embed"\n\n'
        "// RegistryNBT is a length-prefixed (uint32 BE) NBT blob for every entry of\n"
        "// every synchronized registry, in RegistryEntries order.\n"
        "//go:embed registry_nbt.bin\n"
        "var RegistryNBT []byte\n"
    )
    print(f"wrote {outdir}/registry_nbt.bin: {total} entries, {len(blob)} bytes")


if __name__ == "__main__":
    main()
