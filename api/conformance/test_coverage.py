import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("coverage.py")
SPEC = importlib.util.spec_from_file_location("zzira_coverage", MODULE_PATH)
coverage = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(coverage)


class CoverageTest(unittest.TestCase):
    def setUp(self):
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name)
        (self.root / "evidence").mkdir()
        (self.root / "evidence/test.txt").write_text("tested\n")
        self.inventory_path = self.root / "inventory.json"
        self.assessments_path = self.root / "assessments.json"
        self.inventory_path.write_text(
            json.dumps(
                {
                    "snapshotDate": "2026-09-06",
                    "total": 2,
                    "operations": [
                        {
                            "id": "example:GET:/one",
                            "family": "example",
                            "hostClass": "site",
                        },
                        {
                            "id": "example:POST:/two",
                            "family": "example",
                            "hostClass": "central",
                        },
                    ],
                }
            )
        )

    def tearDown(self):
        self.temporary_directory.cleanup()

    def write_assessments(self, assessments):
        self.assessments_path.write_text(
            json.dumps({"schemaVersion": 1, "assessments": assessments})
        )

    def test_assessed_and_unassessed_operations_are_counted_separately(self):
        self.write_assessments(
            [
                {
                    "operationId": "example:GET:/one",
                    "status": "partial",
                    "evidence": ["evidence/test.txt"],
                    "notes": "A useful subset is tested.",
                }
            ]
        )

        result = coverage.build_coverage(
            self.root, self.inventory_path, self.assessments_path
        )

        self.assertEqual(2, result["inventoryTotal"])
        self.assertEqual(1, result["assessedTotal"])
        self.assertEqual(
            {"delivered": 0, "partial": 1, "missing": 0, "unassessed": 1},
            result["counts"],
        )

    def test_unknown_operation_is_rejected(self):
        self.write_assessments(
            [
                {
                    "operationId": "example:GET:/unknown",
                    "status": "missing",
                    "evidence": [],
                    "notes": "Missing.",
                }
            ]
        )

        with self.assertRaisesRegex(ValueError, "unknown operation"):
            coverage.build_coverage(
                self.root, self.inventory_path, self.assessments_path
            )

    def test_partial_operation_requires_existing_evidence(self):
        self.write_assessments(
            [
                {
                    "operationId": "example:GET:/one",
                    "status": "partial",
                    "evidence": ["evidence/missing.txt"],
                    "notes": "A useful subset is tested.",
                }
            ]
        )

        with self.assertRaisesRegex(ValueError, "Evidence does not exist"):
            coverage.build_coverage(
                self.root, self.inventory_path, self.assessments_path
            )


if __name__ == "__main__":
    unittest.main()
