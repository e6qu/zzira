#!/usr/bin/env python3
"""Fail when a pinned container image is behind what Docker Hub serves for it.

Every image this repository pulls is a copy of a Docker Hub image, pinned by
tag and digest: public.ecr.aws/docker/library/<name> for official images and
mirror.gcr.io/<repository> for the rest. A digest that never moves rots, so
each pin is compared with Docker Hub:

  ok      the pin is the digest Docker Hub serves for its tag, and the mirror
          serves that digest too
  HELD    Docker Hub has something newer (a new digest for the tag, or a newer
          release with the same variant), but it is less than 24 hours old
  DRIFT   Docker Hub has had something newer for at least 24 hours, the
          mirror serves a different digest, or the pin is too young: fails

Dependabot cannot do this job: its anonymous lookups on public.ecr.aws exceed
ECR Public's limit of one unauthenticated request per second and fail with 429,
and it applies no cooldown to that registry. This check makes a few requests
per image and spaces its ECR Public requests a second apart.
"""
import datetime as dt
import json
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request

UTC = dt.timezone.utc
WAIT = dt.timedelta(hours=24)
PIN = re.compile(
    r'(public\.ecr\.aws/docker/library|mirror\.gcr\.io)/([a-z0-9._/-]+):([\w.-]+)@(sha256:[0-9a-f]{64})')
VERSION = re.compile(r'(v?)(\d+(?:\.\d+)*)(-[\w.-]+)?')
INDEX_TYPES = ', '.join([
    'application/vnd.oci.image.index.v1+json',
    'application/vnd.docker.distribution.manifest.list.v2+json',
    'application/vnd.oci.image.manifest.v1+json',
    'application/vnd.docker.distribution.manifest.v2+json',
])
ECR_SPACING = 1.1
_last_ecr = 0.0


def timestamp(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00'))


def pins(paths_and_texts):
    """Map (registry, repository, tag, digest) to the files that pin it."""
    found = {}
    for path, text in paths_and_texts:
        for registry, path_in_registry, tag, digest in PIN.findall(text):
            if registry == 'mirror.gcr.io':
                hub = path_in_registry if '/' in path_in_registry else 'library/' + path_in_registry
            else:
                hub = 'library/' + path_in_registry
            found.setdefault((registry, path_in_registry, hub, tag, digest), set()).add(path)
    return found


def newer_tags(tag, candidates):
    """Return candidate (tag, pushed) pairs that are newer releases of the same
    major version and variant as `tag`, newest first. Tags that are not
    versions (`latest`, `alpine`) have no newer releases."""
    match = VERSION.fullmatch(tag)
    if not match:
        return []
    prefix, numbers, suffix = match[1], tuple(map(int, match[2].split('.'))), match[3] or ''
    newer = []
    for name, pushed in candidates:
        other = VERSION.fullmatch(name)
        if not other or other[1] != prefix or (other[3] or '') != suffix:
            continue
        version = tuple(map(int, other[2].split('.')))
        if len(version) == len(numbers) and version[0] == numbers[0] and version > numbers:
            newer.append((version, name, pushed))
    return [(name, pushed) for _, name, pushed in sorted(newer, reverse=True)]


def verdict(pin_digest, hub_digest, hub_pushed, mirror_digest, newer, now):
    """Classify one pin. `newer` is a list of (tag, pushed) newer releases."""
    problems, held = [], []
    if hub_digest == pin_digest and now - hub_pushed < WAIT:
        problems.append(f'the pinned digest is inside the 24-hour quarantine until {(hub_pushed + WAIT).isoformat()}')
    if hub_digest != pin_digest:
        if now - hub_pushed >= WAIT:
            problems.append(f'Docker Hub has served {hub_digest} for the tag since {hub_pushed.isoformat()}')
        else:
            held.append(f'Docker Hub moved the tag to {hub_digest} at {hub_pushed.isoformat()}')
    elif mirror_digest != pin_digest:
        problems.append(f'the mirror serves {mirror_digest} for the tag, not the pinned digest')
    eligible = [(name, pushed) for name, pushed in newer if now - pushed >= WAIT]
    if eligible:
        problems.append(f'newer release {eligible[0][0]} has been on Docker Hub since {eligible[0][1].isoformat()}')
    elif newer:
        held.append(f'newer release {newer[0][0]} is inside the 24-hour quarantine')
    if problems:
        return 'DRIFT', problems
    if held:
        return 'HELD', held
    return 'ok', []


def request(url, headers=None, method='GET'):
    req = urllib.request.Request(url, method=method, headers={'User-Agent': 'zzira-image-pins', **(headers or {})})
    with urllib.request.urlopen(req, timeout=20) as response:
        return response.headers, response.read()


def ecr_request(url, headers=None, method='GET'):
    """ECR Public allows one unauthenticated request per second."""
    global _last_ecr
    wait = _last_ecr + ECR_SPACING - time.monotonic()
    if wait > 0:
        time.sleep(wait)
    try:
        return request(url, headers, method)
    finally:
        _last_ecr = time.monotonic()


def mirror_digest(registry, path, tag):
    headers = {'Accept': INDEX_TYPES}
    if registry == 'public.ecr.aws/docker/library':
        scope = urllib.parse.quote(f'repository:docker/library/{path}:pull', safe='')
        _, body = ecr_request(f'https://public.ecr.aws/token/?scope={scope}&service=public.ecr.aws')
        headers['Authorization'] = 'Bearer ' + json.loads(body)['token']
        reply, _ = ecr_request(f'https://public.ecr.aws/v2/docker/library/{path}/manifests/{tag}', headers, 'HEAD')
    elif registry == 'mirror.gcr.io':
        reply, _ = request(f'https://mirror.gcr.io/v2/{path}/manifests/{tag}', headers, 'HEAD')
    else:
        raise ValueError(f'unsupported image registry: {registry}')
    digest = reply['Docker-Content-Digest']
    if not digest:
        raise ValueError(f'{registry} returned no digest for {path}:{tag}')
    return digest


def hub_tag(hub, tag):
    _, body = request(f'https://hub.docker.com/v2/repositories/{hub}/tags/{urllib.parse.quote(tag, safe="")}')
    data = json.loads(body)
    return data['digest'], timestamp(data['tag_last_pushed'])


def hub_candidates(hub, tag):
    match = VERSION.fullmatch(tag)
    if not match:
        return []
    name = urllib.parse.quote(match[1] + match[2].split('.')[0] + '.', safe='')
    _, body = request(f'https://hub.docker.com/v2/repositories/{hub}/tags?page_size=100&ordering=last_updated&name={name}')
    return [(row['name'], timestamp(row['tag_last_pushed'])) for row in json.loads(body)['results']
            if row.get('tag_last_pushed')]


def tracked_files():
    names = subprocess.run(['git', 'grep', '-lE', PIN.pattern.replace(r'[\w.-]', '[A-Za-z0-9_.-]'), '--', ':!*.md'],
                           capture_output=True, text=True)
    if names.returncode not in (0, 1):
        raise OSError('git grep failed: ' + names.stderr.strip())
    return [name for name in names.stdout.splitlines() if name]


def main():
    now = dt.datetime.now(UTC)
    files = tracked_files()
    found = pins((path, open(path, encoding='utf-8').read()) for path in files)
    if not found:
        print('no pinned images found; the check is not looking at anything', file=sys.stderr)
        return 1
    failed = 0
    for (registry, path, hub, tag, digest), where in sorted(found.items()):
        label = f'{registry}/{path}:{tag} ({", ".join(sorted(where))})'
        try:
            hub_digest, hub_pushed = hub_tag(hub, tag)
            mirrored = mirror_digest(registry, path, tag)
            state, notes = verdict(digest, hub_digest, hub_pushed, mirrored, newer_tags(tag, hub_candidates(hub, tag)), now)
        except (OSError, ValueError, KeyError) as error:
            print(f'ERROR  {label}: could not be checked: {error}', file=sys.stderr)
            failed += 1
            continue
        print(f'{state:<6} {label}' + ''.join('\n         ' + note for note in notes))
        failed += state == 'DRIFT'
    if failed:
        print(f'{failed} image pin(s) are behind Docker Hub or could not be checked. Bump each one to the '
              'digest Docker Hub serves once the mirror serves the same digest.', file=sys.stderr)
        return 1
    print(f'All {len(found)} pinned images are current or held.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
