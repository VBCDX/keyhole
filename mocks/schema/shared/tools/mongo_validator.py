#!/usr/bin/env python3
"""Turn VBCDX JSON Schema (2020-12) files into MongoDB $jsonSchema validators and index commands.

The *.schema.json files are the source of truth. MongoDB's $jsonSchema is a draft-4 subset with
BSON types and no $ref, so this script:
  - inlines every $ref (relative file refs and #/$defs pointers);
  - maps types: string+format date-time -> bsonType date, integer -> int/long,
    number -> int/long/double/decimal, boolean -> bool, null -> null;
  - rewrites const -> enum [value] and if/then/else -> anyOf [not if, then] / anyOf [if, else];
  - drops keywords MongoDB rejects ($schema, $id, $defs, $comment, format, default, examples,
    readOnly, contentEncoding, unevaluatedProperties, x-*).
Dropping unevaluatedProperties makes allOf-composed schemas (connectors, enrollment tokens)
slightly looser in MongoDB than in JSON Schema; the application validates with the full schema.

Usage:
  mongo_validator.py <file.schema.json>            print the $jsonSchema validator
  mongo_validator.py --indexes <indexes.json>      print a mongosh script: createCollection with
                                                   validator, then createIndex for each index
Standard library only.
"""
import json
import os
import sys

_cache = {}
KEEP = {"required", "enum", "minItems", "maxItems", "uniqueItems", "minimum", "maximum", "minLength", "maxLength",
        "pattern", "minProperties", "maxProperties", "description", "title"}
TYPES = {"integer": ["int", "long"], "number": ["int", "long", "double", "decimal"], "boolean": "bool", "null": "null",
         "string": "string", "object": "object", "array": "array"}


def _load(path):
    path = os.path.normpath(path)
    if path not in _cache:
        with open(path) as f:
            _cache[path] = json.load(f)
    return _cache[path]


def _resolve(ref, base):
    url, _, pointer = ref.partition("#")
    path = os.path.normpath(os.path.join(os.path.dirname(base), url)) if url else base
    node = _load(path)
    for part in [p for p in pointer.split("/") if p]:
        node = node[part.replace("~1", "/").replace("~0", "~")]
    return node, path


def _bson_type(t, fmt):
    if isinstance(t, list):
        out = []
        for x in t:
            v = _bson_type(x, fmt)
            out.extend(v if isinstance(v, list) else [v])
        return out
    if t == "string" and fmt == "date-time":
        return "date"
    return TYPES[t]


def convert(node, base, depth=0):
    if depth > 60:
        raise RecursionError("$ref cycle")
    if node is True or node == {}:
        return {}
    if node is False:
        return {"not": {}}
    if "$ref" in node:
        target, tbase = _resolve(node["$ref"], base)
        inner = convert(target, tbase, depth + 1)
        rest = {k: v for k, v in node.items() if k != "$ref"}
        rest_c = convert(rest, base, depth + 1) if any(k not in ("description", "title") and not k.startswith("x-") for k in rest) else {}
        if rest_c:
            return {"allOf": [inner, rest_c]}
        if "description" in rest and "description" not in inner:
            inner = dict(inner, description=rest["description"])
        return inner
    out = {}
    all_of = []
    for k, v in node.items():
        if k == "type":
            out["bsonType"] = _bson_type(v, node.get("format"))
        elif k == "const":
            out["enum"] = [v]
        elif k in KEEP:
            out[k] = v
        elif k in ("properties", "patternProperties"):
            out[k] = {p: convert(s, base, depth + 1) for p, s in v.items()}
        elif k == "additionalProperties":
            out[k] = v if isinstance(v, bool) else convert(v, base, depth + 1)
        elif k == "items":
            out[k] = convert(v, base, depth + 1)
        elif k in ("allOf", "anyOf", "oneOf"):
            out[k] = [convert(s, base, depth + 1) for s in v]
        elif k == "not":
            out[k] = convert(v, base, depth + 1)
        elif k == "if":
            cond = convert(v, base, depth + 1)
            if "then" in node:
                all_of.append({"anyOf": [{"not": cond}, convert(node["then"], base, depth + 1)]})
            if "else" in node:
                all_of.append({"anyOf": [cond, convert(node["else"], base, depth + 1)]})
        # everything else (then/else handled above, $schema, $id, $defs, format, default, examples,
        # readOnly, contentEncoding, unevaluatedProperties, x-*) is dropped.
    if "format" in node and node.get("format") == "date-time" and "type" not in node:
        out["bsonType"] = "date"
    if all_of:
        out["allOf"] = out.get("allOf", []) + all_of
    return out


def validator_for(schema_path, detail_path=None):
    v = convert(_load(schema_path), os.path.normpath(schema_path))
    if detail_path:
        d = convert(_load(detail_path), os.path.normpath(detail_path))
        v = {"allOf": [v, {"properties": {"detail": d}}]}
    return v


def mongosh_script(indexes_path):
    spec = _load(indexes_path)
    here = os.path.dirname(os.path.normpath(indexes_path))
    dbs = spec.get("databases") or {spec["database"]: spec["collections"]}
    lines = ["// Generated by shared/tools/mongo_validator.py from " + os.path.basename(indexes_path)]
    for db, colls in dbs.items():
        lines.append(f"db = db.getSiblingDB({json.dumps(db)});")
        for name, c in colls.items():
            if name.startswith("$"):
                continue
            val = validator_for(os.path.join(here, c["schema"]), os.path.join(here, c["detailSchema"]) if c.get("detailSchema") else None)
            opts = {"validator": {"$jsonSchema": val}, "validationLevel": c["validationLevel"], "validationAction": c["validationAction"]}
            lines.append(f"db.createCollection({json.dumps(name)}, {json.dumps(opts)});")
            for ix in c["indexes"]:
                o = {k: v for k, v in ix.items() if k not in ("key", "global")}
                lines.append(f"db.getCollection({json.dumps(name)}).createIndex({json.dumps(ix['key'])}, {json.dumps(o)});")
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--indexes":
        sys.stdout.write(mongosh_script(sys.argv[2]))
    elif len(sys.argv) == 2:
        json.dump(validator_for(sys.argv[1]), sys.stdout, indent=2)
        sys.stdout.write("\n")
    else:
        sys.exit(__doc__)
