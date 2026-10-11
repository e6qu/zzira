"""Exercise image-pin discovery, release ordering and the 24-hour quarantine."""
import datetime as dt
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('image_pins', Path(__file__).with_name('image-pins.py'))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)

NOW = check.timestamp('2026-10-11T12:00:00Z')
OLD = NOW - dt.timedelta(hours=24)
YOUNG = OLD + dt.timedelta(microseconds=1)
PINNED = 'sha256:' + 'a' * 64
MOVED = 'sha256:' + 'b' * 64


class ImagePinTests(unittest.TestCase):
    def test_pins_name_the_docker_hub_repository_they_copy(self):
        found = check.pins([
            ('Dockerfile', f'FROM public.ecr.aws/docker/library/golang:1.27.2-bookworm@{PINNED} AS build'),
            ('ci.yml', f'driver-opts: image=mirror.gcr.io/moby/buildkit:v0.34.0@{PINNED}'),
            ('compose.yml', 'image: public.ecr.aws/docker/library/postgres:17-alpine'),
        ])
        self.assertEqual(set(found), {
            ('public.ecr.aws/docker/library', 'golang', 'library/golang', '1.27.2-bookworm', PINNED),
            ('mirror.gcr.io', 'moby/buildkit', 'moby/buildkit', 'v0.34.0', PINNED),
        })

    def test_newer_releases_keep_the_variant_and_major_version(self):
        candidates = [('1.27.3-bookworm', OLD), ('1.27.3-alpine', OLD), ('1.28.0-bookworm', OLD),
                      ('2.0.0-bookworm', OLD), ('1.27-bookworm', OLD), ('1.27.1-bookworm', OLD)]
        self.assertEqual([tag for tag, _ in check.newer_tags('1.27.2-bookworm', candidates)],
                         ['1.28.0-bookworm', '1.27.3-bookworm'])
        self.assertEqual(check.newer_tags('latest', candidates), [])
        self.assertEqual([tag for tag, _ in check.newer_tags('v0.33.0', [('v0.34.0', OLD), ('0.35.0', OLD)])], ['v0.34.0'])

    def test_current_pin_served_by_the_mirror_is_ok(self):
        self.assertEqual(check.verdict(PINNED, PINNED, OLD, PINNED, [], NOW), ('ok', []))

    def test_moved_tag_is_held_for_24_hours_then_drifts(self):
        self.assertEqual(check.verdict(PINNED, MOVED, YOUNG, PINNED, [], NOW)[0], 'HELD')
        self.assertEqual(check.verdict(PINNED, MOVED, OLD, PINNED, [], NOW)[0], 'DRIFT')

    def test_newer_release_is_held_for_24_hours_then_drifts(self):
        self.assertEqual(check.verdict(PINNED, PINNED, OLD, PINNED, [('1.27.3', YOUNG)], NOW)[0], 'HELD')
        self.assertEqual(check.verdict(PINNED, PINNED, OLD, PINNED, [('1.27.3', OLD)], NOW)[0], 'DRIFT')

    def test_mirror_serving_another_digest_fails(self):
        self.assertEqual(check.verdict(PINNED, PINNED, OLD, MOVED, [], NOW)[0], 'DRIFT')


if __name__ == '__main__':
    unittest.main()
