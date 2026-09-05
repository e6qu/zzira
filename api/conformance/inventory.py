#!/usr/bin/env python3
"""Generate the operation inventory from checksum-pinned Atlassian OpenAPI.

Run from any directory. --check verifies the committed generated inventory.
The inventory is a scope denominator, not a claim of route or schema coverage.
"""
import argparse
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
METHODS = {"get", "post", "put", "patch", "delete", "head", "options"}


def generate():
    pins = json.loads((ROOT / "api/specs/pins.json").read_text())
    operations = []
    counts = {}
    for pin in pins:
        raw = (ROOT / pin["file"]).read_bytes()
        if hashlib.sha256(raw).hexdigest() != pin["sha256"]:
            raise ValueError(f"Checksum mismatch: {pin['file']}")
        spec = json.loads(raw)
        count = 0
        for path, item in sorted(spec["paths"].items()):
            for method, operation in sorted(item.items()):
                if method not in METHODS:
                    continue
                operations.append({
                    "product": pin["name"],
                    "method": method.upper(),
                    "path": pin["prefix"] + path,
                    "operationId": operation.get("operationId", ""),
                    "groups": operation.get("tags", []),
                    "deprecated": operation.get("deprecated", False),
                })
                count += 1
        counts[pin["name"]] = count
    return json.dumps({"counts": counts, "operations": operations}, indent=2) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    generated = generate()
    destination = ROOT / "api/conformance/cloud-operations.json"
    if args.check:
        if not destination.exists() or destination.read_text() != generated:
            parser.error("Inventory is stale; run python3 api/conformance/inventory.py")
    else:
        destination.write_text(generated)
    counts = json.loads(generated)["counts"]
    print("Pinned operations: " + ", ".join(f"{key}={value}" for key, value in counts.items()))


if __name__ == "__main__":
    main()
