#!/usr/bin/env python3
"""Generate and verify ZZIRA's checksum-pinned Cloud operation inventory.

The inventory is a contract denominator, not a statement of implementation
coverage. Run from any directory. ``--check`` verifies committed output.
"""

import argparse
import hashlib
import json
import re
from collections import Counter
from datetime import date
from pathlib import Path
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_PINS = ROOT / "api/specs/pins.json"
DEFAULT_DESTINATION = ROOT / "api/conformance/cloud-operations.json"
METHODS = {"get", "post", "put", "patch", "delete", "head", "options"}
HOST_CLASSES = {"site", "site-gateway", "central"}
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
REQUIRED_PIN_FIELDS = {
    "name",
    "family",
    "hostClass",
    "file",
    "source",
    "retrieved",
    "sha256",
    "version",
    "prefix",
}


def _validated_pins(root: Path, pins_path: Path) -> list[dict]:
    pins = json.loads(pins_path.read_text())
    if not isinstance(pins, list) or not pins:
        raise ValueError("pins.json must contain a non-empty array")

    names: set[str] = set()
    for index, pin in enumerate(pins):
        if not isinstance(pin, dict):
            raise ValueError(f"Pin {index} must be an object")
        missing = REQUIRED_PIN_FIELDS - pin.keys()
        extra = pin.keys() - REQUIRED_PIN_FIELDS
        if missing or extra:
            raise ValueError(
                f"Pin {index} fields invalid; missing={sorted(missing)}, "
                f"unexpected={sorted(extra)}"
            )
        if not pin["name"] or pin["name"] in names:
            raise ValueError(f"Pin name is empty or duplicated: {pin['name']!r}")
        names.add(pin["name"])
        if pin["hostClass"] not in HOST_CLASSES:
            raise ValueError(
                f"Invalid hostClass for {pin['name']}: {pin['hostClass']!r}"
            )
        if not pin["family"]:
            raise ValueError(f"Family is required for {pin['name']}")
        if pin["prefix"] and not pin["prefix"].startswith("/"):
            raise ValueError(f"Prefix must start with / for {pin['name']}")
        if pin["prefix"].endswith("/"):
            raise ValueError(f"Prefix must not end with / for {pin['name']}")
        if not SHA256_RE.fullmatch(pin["sha256"]):
            raise ValueError(f"Invalid SHA-256 for {pin['name']}")
        try:
            date.fromisoformat(pin["retrieved"])
        except ValueError as error:
            raise ValueError(
                f"Invalid retrieval date for {pin['name']}: {pin['retrieved']!r}"
            ) from error
        parsed_source = urlparse(pin["source"])
        if parsed_source.scheme != "https" or not parsed_source.netloc:
            raise ValueError(f"Source must be an HTTPS URL for {pin['name']}")

        contract_path = (root / pin["file"]).resolve()
        specs_root = (root / "api/specs").resolve()
        if not contract_path.is_relative_to(specs_root):
            raise ValueError(f"Contract file escapes api/specs for {pin['name']}")
        if not contract_path.is_file():
            raise ValueError(f"Contract file does not exist: {pin['file']}")

    return pins


def build_inventory(root: Path = ROOT, pins_path: Path = DEFAULT_PINS) -> dict:
    pins = _validated_pins(root, pins_path)
    operations: list[dict] = []
    contracts: list[dict] = []
    counts: dict[str, int] = {}
    seen_identifiers: set[str] = set()

    for pin in pins:
        contract_path = root / pin["file"]
        raw = contract_path.read_bytes()
        actual_checksum = hashlib.sha256(raw).hexdigest()
        if actual_checksum != pin["sha256"]:
            raise ValueError(
                f"Checksum mismatch for {pin['file']}: "
                f"expected {pin['sha256']}, got {actual_checksum}"
            )

        spec = json.loads(raw)
        paths = spec.get("paths")
        if not isinstance(paths, dict):
            raise ValueError(f"OpenAPI contract has no paths object: {pin['file']}")
        actual_version = str(spec.get("info", {}).get("version", ""))
        if actual_version != pin["version"]:
            raise ValueError(
                f"Version mismatch for {pin['file']}: "
                f"expected {pin['version']!r}, got {actual_version!r}"
            )

        count = 0
        for source_path, path_item in sorted(paths.items()):
            if not isinstance(source_path, str) or not source_path.startswith("/"):
                raise ValueError(f"Invalid OpenAPI path in {pin['file']}: {source_path!r}")
            if not isinstance(path_item, dict):
                raise ValueError(f"Invalid path item in {pin['file']}: {source_path}")
            for method, operation in sorted(path_item.items()):
                if method.lower() not in METHODS:
                    continue
                if not isinstance(operation, dict):
                    raise ValueError(
                        f"Invalid operation in {pin['file']}: {method} {source_path}"
                    )
                canonical_path = pin["prefix"] + source_path
                identifier = f"{pin['name']}:{method.upper()}:{canonical_path}"
                if identifier in seen_identifiers:
                    raise ValueError(f"Duplicate operation identity: {identifier}")
                seen_identifiers.add(identifier)
                operations.append(
                    {
                        "id": identifier,
                        "product": pin["name"],
                        "family": pin["family"],
                        "hostClass": pin["hostClass"],
                        "method": method.upper(),
                        "path": canonical_path,
                        "sourcePath": source_path,
                        "operationId": operation.get("operationId", ""),
                        "groups": operation.get("tags", []),
                        "deprecated": operation.get("deprecated", False),
                    }
                )
                count += 1

        counts[pin["name"]] = count
        contracts.append(
            {
                "name": pin["name"],
                "family": pin["family"],
                "hostClass": pin["hostClass"],
                "version": pin["version"],
                "retrieved": pin["retrieved"],
                "source": pin["source"],
                "file": pin["file"],
                "sha256": pin["sha256"],
                "prefix": pin["prefix"],
                "operationCount": count,
            }
        )

    by_family = Counter(operation["family"] for operation in operations)
    by_host_class = Counter(operation["hostClass"] for operation in operations)
    return {
        "schemaVersion": 2,
        "snapshotDate": max(pin["retrieved"] for pin in pins),
        "total": len(operations),
        "counts": counts,
        "countsByFamily": dict(sorted(by_family.items())),
        "countsByHostClass": dict(sorted(by_host_class.items())),
        "contracts": contracts,
        "operations": operations,
    }


def render_inventory(root: Path = ROOT, pins_path: Path = DEFAULT_PINS) -> str:
    return json.dumps(build_inventory(root, pins_path), indent=2) + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    generated = render_inventory()
    if args.check:
        if not DEFAULT_DESTINATION.exists() or DEFAULT_DESTINATION.read_text() != generated:
            parser.error(
                "Inventory is stale; run python3 api/conformance/inventory.py"
            )
    else:
        DEFAULT_DESTINATION.write_text(generated)

    inventory = json.loads(generated)
    summary = ", ".join(
        f"{key}={value}" for key, value in inventory["counts"].items()
    )
    print(f"Pinned operations ({inventory['total']}): {summary}")


if __name__ == "__main__":
    main()
