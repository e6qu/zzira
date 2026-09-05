#!/usr/bin/env python3
"""Generate operation-level delivery coverage from reviewed assessments."""

import argparse
import json
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
INVENTORY = ROOT / "api/conformance/cloud-operations.json"
ASSESSMENTS = ROOT / "api/conformance/coverage-assessments.json"
DESTINATION = ROOT / "api/conformance/cloud-coverage.json"
STATUSES = {"delivered", "partial", "missing"}


def build_coverage(
    root: Path = ROOT,
    inventory_path: Path = INVENTORY,
    assessments_path: Path = ASSESSMENTS,
) -> dict:
    inventory = json.loads(inventory_path.read_text())
    source = json.loads(assessments_path.read_text())
    if source.get("schemaVersion") != 1:
        raise ValueError("coverage-assessments.json must use schemaVersion 1")
    assessments = source.get("assessments")
    if not isinstance(assessments, list):
        raise ValueError("coverage assessments must be an array")

    known = {operation["id"]: operation for operation in inventory["operations"]}
    reviewed: dict[str, dict] = {}
    for index, assessment in enumerate(assessments):
        if not isinstance(assessment, dict):
            raise ValueError(f"Assessment {index} must be an object")
        expected = {"operationId", "status", "evidence", "notes"}
        if set(assessment) != expected:
            raise ValueError(f"Assessment {index} must contain exactly {sorted(expected)}")
        operation_id = assessment["operationId"]
        if operation_id not in known:
            raise ValueError(f"Assessment references unknown operation: {operation_id}")
        if operation_id in reviewed:
            raise ValueError(f"Operation is assessed twice: {operation_id}")
        if assessment["status"] not in STATUSES:
            raise ValueError(
                f"Invalid status for {operation_id}: {assessment['status']!r}"
            )
        if not isinstance(assessment["notes"], str) or not assessment["notes"].strip():
            raise ValueError(f"Assessment notes are required: {operation_id}")
        evidence = assessment["evidence"]
        if not isinstance(evidence, list) or any(not isinstance(item, str) for item in evidence):
            raise ValueError(f"Assessment evidence must be a string array: {operation_id}")
        if assessment["status"] in {"delivered", "partial"} and not evidence:
            raise ValueError(f"Evidence is required for {assessment['status']}: {operation_id}")
        for evidence_path in evidence:
            resolved = (root / evidence_path).resolve()
            if not resolved.is_relative_to(root.resolve()) or not resolved.is_file():
                raise ValueError(
                    f"Evidence does not exist for {operation_id}: {evidence_path}"
                )
        reviewed[operation_id] = assessment

    status_counts: Counter = Counter()
    family_counts: dict[str, Counter] = defaultdict(Counter)
    operations = []
    for operation in inventory["operations"]:
        assessment = reviewed.get(operation["id"])
        status = assessment["status"] if assessment else "unassessed"
        status_counts[status] += 1
        family_counts[operation["family"]][status] += 1
        operations.append(
            {
                "operationId": operation["id"],
                "family": operation["family"],
                "hostClass": operation["hostClass"],
                "status": status,
                "evidence": assessment["evidence"] if assessment else [],
                "notes": assessment["notes"] if assessment else "",
            }
        )

    ordered_statuses = ["delivered", "partial", "missing", "unassessed"]
    return {
        "schemaVersion": 1,
        "contractSnapshotDate": inventory["snapshotDate"],
        "inventoryTotal": inventory["total"],
        "assessedTotal": len(reviewed),
        "counts": {status: status_counts[status] for status in ordered_statuses},
        "countsByFamily": {
            family: {status: counts[status] for status in ordered_statuses}
            for family, counts in sorted(family_counts.items())
        },
        "operations": operations,
    }


def render_coverage() -> str:
    return json.dumps(build_coverage(), indent=2) + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    generated = render_coverage()
    if args.check:
        if not DESTINATION.exists() or DESTINATION.read_text() != generated:
            parser.error("Coverage is stale; run python3 api/conformance/coverage.py")
    else:
        DESTINATION.write_text(generated)
    coverage = json.loads(generated)
    counts = ", ".join(f"{key}={value}" for key, value in coverage["counts"].items())
    print(
        f"Assessed operations ({coverage['assessedTotal']}/{coverage['inventoryTotal']}): "
        + counts
    )


if __name__ == "__main__":
    main()
