"""Exercise image-pin discovery, release ordering and the 24-hour quarantine."""
import datetime as dt
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('image_pins', Path(__file__).with_name('image-pins.py'))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)

NOW = check.timestamp('2026-10-11T12:00:00Z')
OLD = NOW - dt.timedelta(hours=24)
YOUNG = OLD + dt.timedelta(microseconds=1)
PINNED = 'sha256:' + 'a' * 64
MOVED = 'sha256:' + 'b' * 64


class ImagePinTests(unittest.TestCase):
    def setUp(self):
        check.hub_release_tags.cache_clear()
        check.hub_tag_page.cache_clear()

    def test_eligible_release_on_a_later_page_is_not_hidden_by_the_cooldown(self):
        pages = [
            {'results': [{'name': '1.27.4-bookworm', 'tag_last_pushed': YOUNG.isoformat()}], 'next': '?page=2'},
            {'results': [{'name': '1.27.3-bookworm', 'tag_last_pushed': OLD.isoformat()}], 'next': None},
        ]
        with patch.object(check, 'request', side_effect=[({}, json.dumps(page).encode()) for page in pages]) as request:
            candidates = check.newer_tags('1.27.2-bookworm', check.hub_candidates('library/golang', '1.27.2-bookworm'))
            state, notes = check.verdict(PINNED, PINNED, OLD, PINNED, candidates, NOW)
            self.assertEqual(state, 'DRIFT')
            self.assertIn('1.27.3-bookworm', notes[0])
            self.assertEqual(request.call_count, 2)
            self.assertTrue(request.call_args.args[0].endswith('?page=2'))
            check.hub_candidates('library/golang', '1.27.1-bookworm')
            self.assertEqual(request.call_count, 2)

    def test_large_major_listing_uses_smaller_complete_variant_listing(self):
        pages = [
            {'count': 4414, 'results': [], 'next': '?page=2'},
            {'count': 2, 'results': [{'name': '1.27.4-bookworm', 'tag_last_pushed': YOUNG.isoformat()}], 'next': '?page=2'},
            {'count': 2, 'results': [{'name': '1.27.3-bookworm', 'tag_last_pushed': OLD.isoformat()}], 'next': None},
        ]
        with patch.object(check, 'request', side_effect=[({}, json.dumps(page).encode()) for page in pages]) as request:
            candidates = check.newer_tags('1.27.2-bookworm', check.hub_candidates('library/golang', '1.27.2-bookworm'))
            self.assertEqual([name for name, _ in candidates], ['1.27.4-bookworm', '1.27.3-bookworm'])
            self.assertEqual(request.call_count, 3)
            self.assertTrue(request.call_args_list[1].args[0].endswith('name=-bookworm'))

    def test_pagination_failures_do_not_return_partial_release_metadata(self):
        page = {'results': [{'name': '1.27.4-bookworm', 'tag_last_pushed': YOUNG.isoformat()}], 'next': '?page=2'}
        with patch.object(check, 'request', side_effect=[({}, json.dumps(page).encode()), OSError('unavailable')]):
            with self.assertRaisesRegex(OSError, 'unavailable'):
                check.hub_candidates('library/golang', '1.27.2-bookworm')

    def test_larger_variant_listing_keeps_the_major_family_search(self):
        following = 'https://hub.docker.com/v2/repositories/library/golang/tags?name=1.&page=2'
        pages = [
            {'count': 101, 'results': [], 'next': following},
            {'count': 200, 'results': [], 'next': '?page=2'},
            {'count': 101, 'results': [{'name': '1.27.3-bookworm', 'tag_last_pushed': OLD.isoformat()}], 'next': None},
        ]
        with patch.object(check, 'request', side_effect=[({}, json.dumps(page).encode()) for page in pages]) as request:
            self.assertEqual(check.hub_candidates('library/golang', '1.27.2-bookworm'), [('1.27.3-bookworm', OLD)])
            self.assertEqual(request.call_args.args[0], following)

    def test_missing_publication_time_is_refused(self):
        page = {'results': [{'name': '1.27.3-bookworm'}], 'next': None}
        with patch.object(check, 'request', return_value=({}, json.dumps(page).encode())):
            with self.assertRaisesRegex(ValueError, 'no publication time'):
                check.hub_candidates('library/golang', '1.27.2-bookworm')

    def test_repeated_pagination_link_is_refused(self):
        with patch.object(check, 'request') as request:
            request.side_effect = lambda url: ({}, json.dumps({'results': [], 'next': url}).encode())
            with self.assertRaisesRegex(ValueError, 'repeated pagination link'):
                check.hub_candidates('library/golang', '1.27.2-bookworm')
            self.assertEqual(request.call_count, 1)

    def test_pagination_stays_on_docker_hub_https(self):
        for following in ['https://other.example/tags', 'http://hub.docker.com/tags']:
            check.hub_tag_page.cache_clear()
            with self.subTest(following=following), patch.object(check, 'request', return_value=({}, json.dumps({'results': [], 'next': following}).encode())) as request:
                with self.assertRaisesRegex(ValueError, 'outside its HTTPS API'):
                    check.hub_candidates('library/golang', '1.27.2-bookworm')
                self.assertEqual(request.call_count, 1)

    def test_non_version_tags_do_not_request_release_pages(self):
        with patch.object(check, 'request') as request:
            self.assertEqual(check.hub_candidates('library/golang', 'latest'), [])
            request.assert_not_called()

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

    def test_pinning_a_release_inside_the_cooldown_fails(self):
        self.assertEqual(check.verdict(PINNED, PINNED, YOUNG, PINNED, [], NOW)[0], 'DRIFT')

    def test_discovered_ecr_pin_uses_ecr_token_and_manifest(self):
        registry, path, _, tag, _ = next(iter(check.pins([
            ('Dockerfile', f'FROM public.ecr.aws/docker/library/golang:1.27.2-bookworm@{PINNED}'),
        ])))
        with patch.object(check, 'request') as other, patch.object(check, 'ecr_request') as ecr:
            ecr.side_effect = [({}, b'{"token":"fixture-token"}'),
                               ({'Docker-Content-Digest': PINNED}, b'')]
            self.assertEqual(check.mirror_digest(registry, path, tag), PINNED)
            other.assert_not_called()
            self.assertEqual(ecr.call_args_list[0].args[0],
                             'https://public.ecr.aws/token/?scope=repository%3Adocker%2Flibrary%2Fgolang%3Apull&service=public.ecr.aws')
            url, headers, method = ecr.call_args_list[1].args
            self.assertEqual(url, 'https://public.ecr.aws/v2/docker/library/golang/manifests/1.27.2-bookworm')
            self.assertEqual(headers['Authorization'], 'Bearer fixture-token')
            self.assertEqual(method, 'HEAD')

    def test_gcr_pin_uses_gcr_manifest(self):
        with patch.object(check, 'request', return_value=({'Docker-Content-Digest': PINNED}, b'')) as request:
            self.assertEqual(check.mirror_digest('mirror.gcr.io', 'moby/buildkit', 'v0.34.0'), PINNED)
            self.assertEqual(request.call_args.args[0], 'https://mirror.gcr.io/v2/moby/buildkit/manifests/v0.34.0')
            self.assertEqual(request.call_args.args[2], 'HEAD')

    def test_unknown_registry_is_refused(self):
        with self.assertRaisesRegex(ValueError, 'unsupported image registry'):
            check.mirror_digest('unknown.example', 'golang', '1.27.2')

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
