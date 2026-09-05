import hashlib
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("inventory.py")
SPEC = importlib.util.spec_from_file_location("zzira_inventory", MODULE_PATH)
inventory = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(inventory)


class InventoryTest(unittest.TestCase):
    def setUp(self):
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name)
        (self.root / "api/specs").mkdir(parents=True)
        self.contract = {
            "openapi": "3.0.1",
            "info": {"title": "Fixture", "version": "1.2.3"},
            "paths": {
                "/rest/example": {
                    "get": {
                        "operationId": "getExample",
                        "tags": ["Examples"],
                        "responses": {"200": {"description": "ok"}},
                    }
                }
            },
        }
        contract_bytes = (json.dumps(self.contract) + "\n").encode()
        (self.root / "api/specs/example.json").write_bytes(contract_bytes)
        self.pin = {
            "name": "example",
            "family": "example-family",
            "hostClass": "site",
            "file": "api/specs/example.json",
            "source": "https://example.test/openapi.json",
            "retrieved": "2026-09-06",
            "sha256": hashlib.sha256(contract_bytes).hexdigest(),
            "version": "1.2.3",
            "prefix": "/prefix",
        }
        self.pins_path = self.root / "api/specs/pins.json"
        self.write_pins([self.pin])

    def tearDown(self):
        self.temporary_directory.cleanup()

    def write_pins(self, pins):
        self.pins_path.write_text(json.dumps(pins))

    def test_inventory_contains_stable_contract_and_operation_metadata(self):
        result = inventory.build_inventory(self.root, self.pins_path)

        self.assertEqual(2, result["schemaVersion"])
        self.assertEqual("2026-09-06", result["snapshotDate"])
        self.assertEqual(1, result["total"])
        self.assertEqual({"example": 1}, result["counts"])
        self.assertEqual({"example-family": 1}, result["countsByFamily"])
        self.assertEqual({"site": 1}, result["countsByHostClass"])
        self.assertEqual(1, result["contracts"][0]["operationCount"])
        self.assertEqual(
            {
                "id": "example:GET:/prefix/rest/example",
                "product": "example",
                "family": "example-family",
                "hostClass": "site",
                "method": "GET",
                "path": "/prefix/rest/example",
                "sourcePath": "/rest/example",
                "operationId": "getExample",
                "groups": ["Examples"],
                "deprecated": False,
            },
            result["operations"][0],
        )

    def test_checksum_mismatch_fails_loudly(self):
        bad_pin = dict(self.pin, sha256="0" * 64)
        self.write_pins([bad_pin])

        with self.assertRaisesRegex(ValueError, "Checksum mismatch"):
            inventory.build_inventory(self.root, self.pins_path)

    def test_duplicate_contract_name_is_rejected(self):
        self.write_pins([self.pin, self.pin])

        with self.assertRaisesRegex(ValueError, "duplicated"):
            inventory.build_inventory(self.root, self.pins_path)

    def test_unknown_host_class_is_rejected(self):
        self.write_pins([dict(self.pin, hostClass="magic")])

        with self.assertRaisesRegex(ValueError, "Invalid hostClass"):
            inventory.build_inventory(self.root, self.pins_path)


if __name__ == "__main__":
    unittest.main()
