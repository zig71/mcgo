#!/usr/bin/env python3
"""Projects a light-dampening table for a version whose jar cannot be dumped.

1.21.8 is the odd one out. Mojang's bundler ships the 26.x servers with *named*
classes and 1.21.8 with obfuscated ones — checked: 26.2's inner jar has
net/minecraft/world/level/block/state/BlockBehaviour, 1.21.8's has no
net/minecraft classes at all — so DumpLight, which reads
BlockStateBase.getLightDampening, cannot run against it without the official
mappings and a remapper, and this machine has neither. The block report does not
carry the number either: neither 1.21.8's nor 26.3's report has a light field.

What both versions do have is their state list — every block with its property
values, from the data generator. So this matches each 1.21.8 state to the 26.3
state with the same block and the same properties and takes that state's
dampening. A block whose states all dampen the same can be matched by name alone,
which recovers the blocks whose property set changed between the versions. What
is left is written as 0xff — a value no dampening can have — so the server falls
back to its opacity rule for exactly those states and no others.

This is a projection by name, not the game's own numbers. It is the same
technique the fuel tables use for these two versions (see docs/SPEC.md), and it
is worth having because the alternative was the opacity rule for *every* state.

usage: project_light.py <older report> <newer report> <newer dampening.bin> <out>
"""
import json
import sys

NO_DATA = 0xFF


def states(path):
    """Every state of a report, as (name, frozen properties) -> id, plus ids per block."""
    exact, per_block = {}, {}
    for name, block in json.load(open(path)).items():
        for state in block["states"]:
            props = tuple(sorted((state.get("properties") or {}).items()))
            exact[(name, props)] = state["id"]
            per_block.setdefault(name, []).append(state["id"])
    return exact, per_block


def main() -> None:
    target_report, donor_report, donor_bin, out_path = sys.argv[1:5]
    # The target is what is being built (1.21.8); the donor is where the numbers
    # come from (26.3). Getting these the wrong way round produces a table
    # indexed by the donor's state ids, which is worse than no table at all:
    # every id in the target would then be read at a stranger's offset.
    target, _ = states(target_report)
    donor, donor_ids = states(donor_report)
    dampening = open(donor_bin, "rb").read()

    uniform = {}
    for name, ids in donor_ids.items():
        values = {dampening[i] for i in ids}
        if len(values) == 1:
            uniform[name] = values.pop()

    size = max(target.values()) + 1
    out = bytearray([NO_DATA]) * size
    matched = by_block = 0
    missed = {}
    for (name, props), state_id in target.items():
        if (name, props) in donor:
            out[state_id] = dampening[donor[(name, props)]]
            matched += 1
        elif name in uniform:
            out[state_id] = uniform[name]
            by_block += 1
        else:
            missed.setdefault(name, 0)
            missed[name] += 1

    open(out_path, "wb").write(bytes(out))
    print(f"{size} states: {matched} matched by block and properties, "
          f"{by_block} by block alone, {len(missed)} blocks left to the opacity fallback")
    if missed:
        print("  no counterpart:", ", ".join(sorted(missed)[:12]))


if __name__ == "__main__":
    main()
