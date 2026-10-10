"""Exercise publication boundaries and fail-closed dependency identification."""
import datetime as dt
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('dependency_age', Path(__file__).with_name('dependency-age.py'))
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


class DependencyAgeTests(unittest.TestCase):
    def test_exact_24_hours_is_eligible(self):
        published = policy.timestamp('2026-10-09T12:00:00Z')
        policy.require_age(published, published + dt.timedelta(hours=24))
        with self.assertRaises(ValueError):
            policy.require_age(published, published + dt.timedelta(hours=24) - dt.timedelta(microseconds=1))

    def test_timezone_offsets_and_future_publications(self):
        published = policy.timestamp('2026-10-09T14:00:00+02:00')
        policy.require_age(published, policy.timestamp('2026-10-10T12:00:00Z'))
        with self.assertRaises(ValueError):
            policy.require_age(published, published - dt.timedelta(seconds=1))

    def test_transitive_npm_packages_are_included(self):
        files = {'go.mod': '', 'e2e/package-lock.json': '''{"packages": {
            "": {}, "node_modules/@playwright/test": {"version": "1.64.0"},
            "node_modules/playwright/node_modules/example": {"version": "2.0.0"}}}'''}
        dependencies = policy.inventory(lambda path: files.get(path, ''), [])
        self.assertIn(('npm', '@playwright/test', '1.64.0'), dependencies)
        self.assertIn(('npm', 'example', '2.0.0'), dependencies)

    def test_floating_actions_images_and_node_versions_are_refused(self):
        path = '.github/workflows/example.yml'
        for text in ['- uses: actions/checkout@v7', 'image: postgres:17-alpine', 'node-version: "24"']:
            with self.subTest(text=text), self.assertRaises(ValueError):
                policy.inventory(lambda file: text if file == path else '', [path])

    def test_pip_versions_must_be_exact(self):
        path = '.github/test-requirements.txt'
        with self.assertRaises(ValueError):
            policy.inventory(lambda file: 'semgrep>=1.0' if file == path else '', [path])

    @patch.object(policy, 'fetch')
    def test_new_wheel_upload_does_not_inherit_an_older_upload_date(self, fetch):
        fetch.return_value = {'urls': [
            {'yanked': False, 'upload_time_iso_8601': '2026-10-01T00:00:00Z'},
            {'yanked': False, 'upload_time_iso_8601': '2026-10-10T00:00:00Z'},
        ]}
        published = policy.publication(('pip', 'example', '1.0.0'))
        with self.assertRaises(ValueError):
            policy.require_age(published, policy.timestamp('2026-10-10T12:00:00Z'))
        fetch.return_value['urls'][0]['yanked'] = True
        with self.assertRaises(ValueError):
            policy.publication(('pip', 'example', '1.0.0'))

    @patch.object(policy, 'fetch')
    def test_an_action_must_match_its_release_commit(self, fetch):
        fetch.side_effect = [
            {'draft': False, 'prerelease': False, 'published_at': '2026-10-01T00:00:00Z'},
            {'object': {'type': 'commit', 'sha': 'b' * 40}},
        ]
        with self.assertRaises(ValueError):
            policy.publication(('action', 'actions/checkout', 'v7.0.1@' + 'a' * 40))

    @patch.object(policy, 'fetch')
    def test_retagged_image_cannot_borrow_the_new_tags_publication_date(self, fetch):
        fetch.return_value = {'digest': 'sha256:' + 'b' * 64, 'tag_last_pushed': '2026-10-01T00:00:00Z'}
        with self.assertRaises(ValueError):
            policy.publication(('docker', 'library/golang', '1.27.2@sha256:' + 'a' * 64))

    @patch.object(policy, 'fetch')
    def test_node_date_is_treated_as_the_end_of_the_publication_day(self, fetch):
        fetch.return_value = [{'version': 'v24.21.0', 'date': '2026-10-09'}]
        published = policy.publication(('node', 'node', '24.21.0'))
        with self.assertRaises(ValueError):
            policy.require_age(published, policy.timestamp('2026-10-10T23:59:59Z'))
        policy.require_age(published, policy.timestamp('2026-10-11T00:00:00Z'))


if __name__ == '__main__':
    unittest.main()
