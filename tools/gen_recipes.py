#!/usr/bin/env python3
"""Emit cooking recipes and fuel values (recipes_gen.go).

Usage: gen_recipes.py <reports_dir> <server_jar> <out_dir> [fuel_reports_dir [fuel_jar]]

Ingredients are resolved to raw item IDs here, once, so the server can look a
recipe up by the item sitting in the input slot.

Fuel values live in the item data (minecraft:cooking_fuel -> a burn_time
context-int-provider). The provider divides a base by the "fast cooking" factor
(2 inside a blast furnace or smoker), which is why each value is emitted twice.

1.21.8 predates cooking_fuel components, so its fuel table is taken by item
NAME from the version named by fuel_reports_dir (the newest data generator
report). Burn times have been stable across these versions.
"""
import io
import json
import sys
import zipfile
from pathlib import Path

CRAFTING_KINDS = ("minecraft:crafting_shaped", "minecraft:crafting_shapeless")

COOKING_KINDS = {
    "minecraft:smelting": "FurnaceRecipes",
    "minecraft:blasting": "BlastRecipes",
    "minecraft:smoking": "SmokerRecipes",
    "minecraft:campfire_cooking": "CampfireRecipes",
}

# Items whose crafting remainder is a different item. These are Item.Properties
# calls in Items.java, not data, so they cannot be generated from a report.
# Verified against net/minecraft/world/item/Items.java (craftRemainder).
REMAINDERS = {
    "minecraft:water_bucket": "minecraft:bucket",
    "minecraft:lava_bucket": "minecraft:bucket",
    "minecraft:milk_bucket": "minecraft:bucket",
    "minecraft:dragon_breath": "minecraft:glass_bottle",
    "minecraft:honey_bottle": "minecraft:glass_bottle",
}


def data_zip(jar_path: str) -> zipfile.ZipFile:
    z = zipfile.ZipFile(jar_path)
    inner = next(n for n in z.namelist() if n.startswith("META-INF/versions/") and n.endswith(".jar"))
    return zipfile.ZipFile(io.BytesIO(z.read(inner)))


def item_ids(reports: Path) -> dict:
    regs = json.loads((reports / "registries.json").read_text())
    return {name: e["protocol_id"] for name, e in regs["minecraft:item"]["entries"].items()}


def item_tags(iz: zipfile.ZipFile) -> dict:
    """tag name -> set of item names, with #references resolved."""
    raw = {}
    for n in iz.namelist():
        if n.startswith("data/minecraft/tags/item/") and n.endswith(".json"):
            tag = "minecraft:" + n[len("data/minecraft/tags/item/") : -len(".json")]
            raw[tag] = json.loads(iz.read(n)).get("values", [])

    resolved = {}

    def expand(tag, seen):
        if tag in resolved:
            return resolved[tag]
        if tag in seen or tag not in raw:
            return set()
        seen.add(tag)
        out = set()
        for v in raw[tag]:
            name = v["id"] if isinstance(v, dict) else v
            if name.startswith("#"):
                out |= expand(name[1:] if ":" in name[1:] else "minecraft:" + name[1:], seen)
            else:
                out.add(name)
        resolved[tag] = out
        return out

    for tag in raw:
        expand(tag, set())
    return resolved


def ingredient_ids(value, ids, tags) -> set:
    """One recipe ingredient (item, #tag, or list of either) -> item names."""
    out = set()
    items = value if isinstance(value, list) else [value]
    for v in items:
        if isinstance(v, dict):
            v = v.get("id") or v.get("item")
        if not isinstance(v, str):
            continue
        if v.startswith("#"):
            out |= tags.get(v[1:] if ":" in v[1:] else "minecraft:" + v[1:], set())
        else:
            out.add(v)
    return out


def fuel_by_name(src_reports: Path, iz: zipfile.ZipFile) -> dict:
    """item name -> burn ticks on a normal furnace, from cooking_fuel.

    burn_time names a context-int-provider that lives in the jar's data pack
    (data/minecraft/context_int_provider/cooking/*.json), not in the reports.
    """
    out = {}
    d = src_reports / "minecraft" / "components" / "item"
    if not d.is_dir():
        return out

    providers = {}

    def provider(name, depth=0):
        if depth > 4:
            return None
        if name in providers:
            return providers[name]
        rel = "data/minecraft/context_int_provider/" + name[10:] + ".json"
        if rel not in iz.namelist():
            return None
        d = json.loads(iz.read(rel))
        val = None
        if isinstance(d, int):
            val = d
        elif isinstance(d, dict):
            left = d.get("left")
            if isinstance(left, int):
                # The right side is the fast-cooking divisor: 1 normally, 2 in
                # a blast furnace or smoker. Normal-furnace value is left/1.
                val = left
            elif isinstance(left, str):
                val = provider(left, depth + 1)
        providers[name] = val
        return val

    for f in sorted(d.iterdir()):
        comp = json.loads(f.read_text())["components"].get("minecraft:cooking_fuel")
        if not comp:
            continue
        burn = comp.get("burn_time")
        if isinstance(burn, int):
            out["minecraft:" + f.stem] = burn
        elif isinstance(burn, str):
            v = provider(burn)
            if v:
                out["minecraft:" + f.stem] = v
    return out


def main() -> None:
    reports, jar, outdir = Path(sys.argv[1]), sys.argv[2], Path(sys.argv[3])
    fuel_src = Path(sys.argv[4]) if len(sys.argv) > 4 else reports
    fuel_jar = sys.argv[5] if len(sys.argv) > 5 else jar
    pkg = outdir.name
    ids = item_ids(reports)
    iz = data_zip(jar)
    tags = item_tags(iz)

    # Cooking recipes, keyed by input item id. Several recipes can accept the
    # same item; the first (alphabetically, so it is deterministic) wins.
    by_kind = {k: {} for k in COOKING_KINDS}
    for n in sorted(iz.namelist()):
        if not (n.startswith("data/minecraft/recipe/") and n.endswith(".json")):
            continue
        d = json.loads(iz.read(n))
        var = COOKING_KINDS.get(d.get("type"))
        if var is None or "ingredient" not in d:
            continue
        result = d.get("result") or {}
        rid = ids.get(result.get("id"))
        if not rid:
            continue
        for name in ingredient_ids(d["ingredient"], ids, tags):
            iid = ids.get(name)
            if iid is None or iid in by_kind[d["type"]]:
                continue
            by_kind[d["type"]][iid] = (rid, result.get("count", 1), d.get("cookingtime", 200))

    # A blast furnace and a smoker cook twice as fast. Some versions state the
    # base time in the recipe (200) and halve it in the machine; others bake
    # the halving into the recipe (100). Telling them apart from a known recipe
    # lets both produce the same, effective, number here, so the server never
    # has to know which rule its version follows.
    probe = "data/minecraft/recipe/iron_ingot_from_blasting_raw_iron.json"
    base_times = False
    if probe in iz.namelist():
        base_times = json.loads(iz.read(probe)).get("cookingtime", 200) > 150
    if base_times:
        for kind in ("minecraft:blasting", "minecraft:smoking"):
            by_kind[kind] = {
                i: (r, c, max(1, -(-t // 2))) for i, (r, c, t) in by_kind[kind].items()
            }

    # Crafting recipes. A slot is an index+1 into a shared table of acceptable
    # item sets, 0 for "must be empty"; tag ingredients (#minecraft:planks) all
    # share one entry, which keeps the table small.
    ingredient_sets = []
    ingredient_index = {}

    def set_index(names):
        key = tuple(sorted(names))
        if not key:
            return 0
        if key not in ingredient_index:
            ingredient_index[key] = len(ingredient_sets) + 1
            ingredient_sets.append([ids[n] for n in key if n in ids])
        return ingredient_index[key]

    shaped, shapeless = [], []
    for n in sorted(iz.namelist()):
        if not (n.startswith("data/minecraft/recipe/") and n.endswith(".json")):
            continue
        d = json.loads(iz.read(n))
        if d.get("type") not in CRAFTING_KINDS:
            continue
        result = d.get("result") or {}
        rid = ids.get(result.get("id"))
        if not rid:
            continue
        count = result.get("count", 1)
        if d["type"] == "minecraft:crafting_shaped":
            rows = d.get("pattern") or []
            key = d.get("key") or {}
            width = max((len(r) for r in rows), default=0)
            cells = []
            for row in rows:
                for x in range(width):
                    ch = row[x] if x < len(row) else " "
                    cells.append(set_index(ingredient_ids(key[ch], ids, tags)) if ch != " " else 0)
            shaped.append((width, len(rows), cells, rid, count, n))
        else:
            cells = [set_index(ingredient_ids(v, ids, tags)) for v in d.get("ingredients") or []]
            shapeless.append((cells, rid, count, n))

    fuel_names = fuel_by_name(fuel_src, iz if fuel_jar == jar else data_zip(fuel_jar))
    remaining = {ids[k]: ids[v] for k, v in REMAINDERS.items() if k in ids and v in ids}

    out = [
        "// Code generated by tools/gen_recipes.py. DO NOT EDIT.",
        "//",
        "// Cooking recipes keyed by input item id, and fuel burn times in ticks.",
        "",
        f"package {pkg}",
        "",
        f'import "{MODULE}/internal/proto"',
        "",
    ]
    for kind, var in COOKING_KINDS.items():
        out.append(f"// {var}: {kind[10:]} recipes, keyed by input item id.")
        out.append(f"var {var} = map[int32]proto.CookingRecipe{{")
        for iid in sorted(by_kind[kind]):
            rid, count, time = by_kind[kind][iid]
            out.append(f"\t{iid}: {{Result: {rid}, Count: {count}, Time: {time}}},")
        out.append("}")
        out.append("")
    out.append("// Fuel maps an item id to how many ticks it burns, in a normal")
    out.append("// furnace. A blast furnace or smoker halves it (its fuel burns twice")
    out.append("// as fast) and cooks twice as fast, so the server applies the same")
    out.append("// factor to both sides.")
    out.append("var Fuel = map[int32]int32{")
    for name in sorted(fuel_names):
        iid = ids.get(name)
        if iid is None:
            continue
        out.append(f"\t{iid}: {fuel_names[name]},")
    out.append("}")
    out.append("")
    out.append("// CraftingIngredients holds the distinct acceptable-item sets that")
    out.append("// crafting recipes refer to; a recipe slot stores an index+1 into this")
    out.append("// table, 0 meaning the cell must be empty.")
    out.append("var CraftingIngredients = [][]int32{")
    for group in ingredient_sets:
        out.append("\t{" + ", ".join(str(i) for i in group) + "},")
    out.append("}")
    out.append("")
    out.append("// CraftingRecipes are the shaped and shapeless recipes from the data")
    out.append("// pack. Shaped recipes state their width and height and a flattened")
    out.append("// grid; shapeless ones only list their ingredients.")
    out.append("var CraftingRecipes = []proto.CraftingRecipe{")
    for width, height, cells, rid, count, n in shaped:
        out.append(
            f"\t{{Width: {width}, Height: {height}, Slots: []int32{{{', '.join(map(str, cells))}}}, Result: {rid}, Count: {count}}}, // {n}"
        )
    for cells, rid, count, n in shapeless:
        out.append(
            f"\t{{Shapeless: true, Slots: []int32{{{', '.join(map(str, cells))}}}, Result: {rid}, Count: {count}}}, // {n}"
        )
    out.append("}")
    out.append("")
    out.append("// CraftRemainder maps an item id to what is left in the slot after")
    out.append("// it is consumed (a lava bucket leaves an empty bucket).")
    out.append("var CraftRemainder = map[int32]int32{")
    for a in sorted(remaining):
        out.append(f"\t{a}: {remaining[a]},")
    out.append("}")
    outdir.mkdir(parents=True, exist_ok=True)
    (outdir / "recipes_gen.go").write_text("\n".join(out) + "\n")
    print(
        f"wrote {outdir}/recipes_gen.go: "
        + ", ".join(f"{k[10:]}={len(v)}" for k, v in by_kind.items())
        + f", fuel={len(fuel_names)}, shaped={len(shaped)}, shapeless={len(shapeless)}"
    )


MODULE = "mcgo"

if __name__ == "__main__":
    main()
