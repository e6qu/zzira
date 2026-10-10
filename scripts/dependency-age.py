#!/usr/bin/env python3
"""Reject changed dependency releases younger than 24 hours, including security PRs."""
import argparse
import datetime as dt
import functools
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import urllib.parse
import urllib.request

UTC = dt.timezone.utc
WAIT = dt.timedelta(hours=24)


def timestamp(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00'))


def require_age(published, now):
    eligible = published + WAIT
    if now < eligible:
        raise ValueError(f'eligible at {eligible.isoformat()} (published {published.isoformat()})')


@functools.lru_cache(maxsize=None)
def fetch(url):
    headers = {'User-Agent': 'zzira-dependency-age', 'Accept': 'application/json'}
    if url.startswith('https://api.github.com/') and os.environ.get('GITHUB_TOKEN'):
        headers['Authorization'] = 'Bearer ' + os.environ['GITHUB_TOKEN']
    elif url.startswith('https://api.github.com/') and shutil.which('gh'):
        result = subprocess.run(['gh', 'api', url], capture_output=True, text=True)
        if result.returncode:
            raise OSError('GitHub publication metadata unavailable: ' + result.stderr.strip())
        return json.loads(result.stdout)
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=20) as response:
        return json.load(response)


def inventory(read, paths):
    result = set()
    mod = read('go.mod')
    for name, version in re.findall(r'^\s*([^\s]+) (v[^\s]+)', mod, re.M):
        result.add(('go', name, version))
    match = re.search(r'^go (\d+\.\d+\.\d+)$', mod, re.M)
    if match:
        result.add(('go', 'golang.org/toolchain', f'v0.0.1-go{match[1]}.linux-amd64'))
    lock = read('e2e/package-lock.json')
    if lock:
        for path, package in json.loads(lock).get('packages', {}).items():
            if 'node_modules/' in path:
                result.add(('npm', path.rsplit('node_modules/', 1)[1], package['version']))
    for path in paths:
        text = read(path)
        if path.startswith('.github/') and path.endswith('-requirements.txt'):
            for line in text.splitlines():
                if line.strip() and not line.startswith('#'):
                    match = re.fullmatch(r'([\w.-]+)==([\w.]+)', line)
                    if not match:
                        raise ValueError(f'{path}: dependencies must use exact versions')
                    result.add(('pip', match[1], match[2]))
        if path.startswith('.github/workflows/'):
            for name, revision, version in re.findall(r'uses: ([\w.-]+/[\w./-]+)@([^\s]+)(?:\s+#\s*(v[\d.]+))?', text):
                if not re.fullmatch(r'[a-f0-9]{40}', revision) or not version:
                    raise ValueError(f'{path}: {name} needs a commit SHA and release-version comment')
                result.add(('action', '/'.join(name.split('/')[:2]), version + '@' + revision))
            for version in re.findall(r'node-version: [\"\']?([^\s\"\']+)', text):
                if not re.fullmatch(r'\d+\.\d+\.\d+', version):
                    raise ValueError(f'{path}: Node needs an exact version')
                result.add(('node', 'node', version))
            for version in re.findall(r'golang.org/x/vuln/cmd/govulncheck@(v[\d.]+)', text):
                result.add(('go', 'golang.org/x/vuln', version))
            if 'golangci-lint-action@' in text:
                for version in re.findall(r'version: (v2\.[\d.]+)', text):
                    result.add(('release', 'golangci/golangci-lint', version))
            if 'trivy-action@' in text:
                for version in re.findall(r'version: (v0\.[\d.]+)', text):
                    result.add(('release', 'aquasecurity/trivy', version))
        for image in re.findall(r'(?:image[:=]\s*|FROM\s+(?:--platform=\S+\s+)?)([^\s]+)', text):
            if image == 'scratch' or '${' in image:
                continue
            normalized = image.removeprefix('public.ecr.aws/docker/').removeprefix('mirror.gcr.io/')
            match = re.fullmatch(r'([\w./-]+):([^@]+)@(sha256:[a-f0-9]{64})', normalized)
            if not match:
                raise ValueError(f'{path}: image {image} needs a version and digest')
            result.add(('docker', match[1], match[2] + '@' + match[3]))
    return result


def publication(dependency):
    kind, name, version = dependency
    quoted = urllib.parse.quote(version, safe='')
    if kind == 'go':
        escaped = ''.join('!' + c.lower() if c.isupper() else c for c in name)
        return timestamp(fetch(f'https://proxy.golang.org/{escaped}/@v/{quoted}.info')['Time'])
    if kind == 'npm':
        return timestamp(fetch('https://registry.npmjs.org/' + urllib.parse.quote(name, safe=''))['time'][version])
    if kind == 'pip':
        files = fetch(f'https://pypi.org/pypi/{name}/{quoted}/json')['urls']
        if not files or any(file['yanked'] for file in files):
            raise ValueError('missing or yanked distributions')
        return max(timestamp(file['upload_time_iso_8601']) for file in files)
    if kind == 'node':
        release = next(item for item in fetch('https://nodejs.org/dist/index.json') if item['version'] == 'v' + version)
        # Node records a date, not a time: use the end of that UTC day.
        return timestamp(release['date'] + 'T00:00:00+00:00') + dt.timedelta(days=1)
    if kind in ('action', 'release'):
        tag = version.split('@')[0]
        release = fetch(f'https://api.github.com/repos/{name}/releases/tags/{urllib.parse.quote(tag, safe="")}')
        if release['draft'] or release['prerelease']:
            raise ValueError('not a stable published release')
        if kind == 'action':
            ref = fetch(f'https://api.github.com/repos/{name}/git/ref/tags/{urllib.parse.quote(tag, safe="")}')['object']
            while ref['type'] == 'tag':
                ref = fetch(ref['url'])['object']
            if ref['type'] != 'commit' or ref['sha'] != version.split('@')[1]:
                raise ValueError('commit does not match the documented release tag')
        return timestamp(release['published_at'])
    if kind == 'docker':
        tag, digest = version.split('@')
        data = fetch(f'https://hub.docker.com/v2/repositories/{name}/tags/{urllib.parse.quote(tag, safe="")}')
        if data['digest'] != digest:
            raise ValueError('digest does not match current upstream tag; verify its publication before updating')
        return timestamp(data['tag_last_pushed'])
    raise ValueError('unknown ecosystem ' + kind)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True, help='Pull request base SHA')
    args = parser.parse_args()
    subprocess.run(['git', 'cat-file', '-e', args.base + '^{commit}'], check=True)
    paths = ['go.mod', 'e2e/package-lock.json', 'Dockerfile', 'docker-compose.yml']
    paths += [str(path) for path in Path('.github').rglob('*') if path.suffix in ('.yml', '.txt')]

    @functools.lru_cache(maxsize=None)
    def previous(path):
        result = subprocess.run(['git', 'show', f'{args.base}:{path}'], capture_output=True, text=True)
        return result.stdout if result.returncode == 0 else ''

    # Older branches used floating action/image tags. They are only a baseline;
    # the current manifests must be pinned before any new dependencies execute.
    def old_inventory():
        old_paths = [path for path in paths if path in ('go.mod', 'e2e/package-lock.json') or path.endswith('-requirements.txt')]
        return inventory(previous, old_paths)

    now = dt.datetime.now(UTC)
    try:
        current = inventory(lambda path: Path(path).read_text(), paths)
        before = old_inventory()
        # Keep immutable actions/images from the base out of the network check.
        for dependency in current:
            kind, name, version = dependency
            if kind not in ('go', 'npm', 'pip'):
                if kind == 'docker' and any(version in previous(path) for path in paths):
                    before.add(dependency)
                elif kind == 'action' and any('@' + version.split('@')[1] + ' # ' + version.split('@')[0] in previous(path) for path in paths):
                    before.add(dependency)
        failed = False
        for dependency in sorted(current - before):
            label = ' '.join(dependency)
            try:
                require_age(publication(dependency), now)
                print('Eligible:', label)
            except (ValueError, KeyError, StopIteration, OSError) as error:
                failed = True
                print(f'REJECTED: {label}: {error}', file=sys.stderr)
        return int(failed)
    except (ValueError, KeyError) as error:
        print(str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
