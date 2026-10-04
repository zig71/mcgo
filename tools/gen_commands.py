#!/usr/bin/env python3
"""Emits the command tree as the exact bytes of the Commands packet.

The tree never changes for a given version, and its encoding has a dozen cases
(a byte of flags, a minimum, an identifier) rather than the thirty argument
types it looks like. Emitting the bytes here means the server sends a blob, and
none of that lives in Go.

usage: gen_commands.py <reports dir> <out dir> <package> <argument type ids json>
"""
import json
import os
import struct
import sys


def varint(value):
    out = bytearray()
    while True:
        if value & ~0x7F == 0:
            out.append(value)
            return bytes(out)
        out.append((value & 0x7F) | 0x80)
        value >>= 7


def identifier(s):
    b = s.encode()
    return varint(len(b)) + b


def string(s):
    b = s.encode()
    return varint(len(b)) + b


def main():
    reports, outdir, package, ids_path = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    tree = json.load(open(os.path.join(reports, "commands.json")))
    type_ids = json.load(open(ids_path))

    # Flatten the nested tree into the wire order, remembering each node's
    # index so children and redirects can point at it.
    nodes = []
    index = {}

    def flatten(node, path):
        index[path] = len(nodes)
        nodes.append((node, path))
        ch = node.get("children")
        kids = list(ch.items()) if isinstance(ch, dict) else [(None, c) for c in (ch or [])]
        for name, child in kids:
            flatten(child, path + (name,) if name else path)

    flatten(tree, ())

    def resolve_redirect(path):
        # The report gives a redirect as a path of names from the root.
        for node, node_path in nodes:
            if node_path == tuple(path):
                return index[tuple(node_path)]
        return None

    body = bytearray()
    body += varint(len(nodes))
    offsets = []
    for node, path in nodes:
        offsets.append("\t".join((str(len(offsets)), "/".join(path) or "(root)", str(len(body))))) 
        flags = {"root": 0, "literal": 1, "argument": 2}[node["type"]]
        if node.get("executable"):
            flags |= 4
        redirect = node.get("redirect")
        if redirect:
            flags |= 8
        body.append(flags)

        ch = node.get("children")
        if isinstance(ch, dict):
            kids = list(ch.items())
        else:
            kids = [(None, c) for c in (ch or [])]
        body += varint(len(kids))
        for child_name, _child in kids:
            # A child's index is the one its own path was given when the tree
            # was flattened, and the flatten walks children in this same order.
            body += varint(index.get(path + (child_name,) if child_name else path, 0))

        # The redirect comes BEFORE the name: vanilla writes the flags, the
        # children, then the redirect, and only then the node's own stub. Doing
        # it the other way round is invisible until the first redirected node,
        # which is a thousand nodes into the tree.
        if redirect:
            body += varint(resolve_redirect(redirect) or 0)
        if node["type"] != "root":
            body += string(path[-1] if path else "")
        if node["type"] == "argument":
            parser = node["parser"]
            body += varint(type_ids[parser])
            props = node.get("properties", {})
            if parser.startswith("brigadier:") and parser.split(":")[1] in ("integer", "long", "double", "float"):
                kind = parser.split(":")[1]
                has_min, has_max = "min" in props, "max" in props
                body.append((1 if has_min else 0) | (2 if has_max else 0))
                for present, key in ((has_min, "min"), (has_max, "max")):
                    if not present:
                        continue
                    if kind == "integer":
                        body += int(props[key]).to_bytes(4, "big", signed=True)
                    elif kind == "long":
                        body += int(props[key]).to_bytes(8, "big", signed=True)
                    elif kind == "double":
                        # DoubleArgumentInfo writes a double, not a float: four
                        # bytes short here and every later node in the packet is
                        # read from the wrong offset, which is the "failed to
                        # decode packet" a client reports.
                        body += struct.pack(">d", float(props[key]))
                    else:
                        body += struct.pack(">f", float(props[key]))
            elif parser == "brigadier:string":
                # The report names the type as the serializer's JSON does
                # (word/phrase/greedy); the wire carries the enum's ordinal.
                body += varint({"word": 0, "phrase": 1, "greedy": 2}.get(props.get("type", "word"), 0))
            elif parser == "minecraft:entity":
                entity_flags = (1 if props.get("amount") == "single" else 0) | (2 if props.get("type") == "players" else 0)
                body.append(entity_flags)
            elif parser == "minecraft:score_holder":
                body.append(1 if props.get("amount") == "multiple" else 0)
            elif parser in ("minecraft:resource", "minecraft:resource_key", "minecraft:resource_or_tag",
                            "minecraft:resource_or_tag_key", "minecraft:resource_selector"):
                body += identifier(props["registry"])
            elif parser == "minecraft:time":
                body += int(props.get("min", 0)).to_bytes(4, "big", signed=True)

    # The root index, which the packet carries AFTER the nodes: without it a
    # client reads past the end of the packet.
    body += varint(index[()])

    open(os.path.join(outdir, "commands.bin"), "wb").write(bytes(body))
    open(os.path.join(outdir, "commands_offsets.txt"), "w").write("\n".join(offsets) + "\n")
    open(os.path.join(outdir, "commands_gen.go"), "w").write(
        "// Code generated by tools/gen_commands.py. DO NOT EDIT.\n\n"
        "package %s\n\n"
        "import _ \"embed\"\n\n"
        "//go:embed commands.bin\n"
        "var CommandsBytes []byte\n" % package)
    print("wrote %d nodes, %d bytes" % (len(nodes), len(body)))


main()
