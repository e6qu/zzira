import { expect, test, APIRequestContext } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

const headers = () => ({ Authorization: apiAuthHeader() });

async function expectImage(request: APIRequestContext, url: string) {
  const response = await request.get(new URL(url, 'http://placeholder').pathname, { headers: headers() });
  expect(response.status(), url).toBe(200);
  expect(response.headers()['content-type'], url).toMatch(/^image\//);
}

test('every issue type, priority and avatar icon a client is given resolves to an image', async ({ request }) => {
  for (const resource of ['/rest/api/3/issuetype', '/rest/api/3/priority']) {
    const response = await request.get(resource, { headers: headers() });
    expect(response.status()).toBe(200);
    const values: { iconUrl: string; avatarId?: number }[] = await response.json();
    expect(values.length).toBeGreaterThan(0);
    for (const value of values) {
      await expectImage(request, value.iconUrl);
    }
  }
  for (const type of ['project', 'issuetype', 'priority']) {
    const system = await request.get(`/rest/api/3/avatar/${type}/system`, { headers: headers() });
    expect(system.status()).toBe(200);
    const { system: avatars } = await system.json();
    for (const avatar of avatars) {
      await expectImage(request, avatar.urls['48x48']);
    }
  }
});

test('people journey: myself, locale, user search, structured query and a group swapped on delete', async ({ request }) => {
  const me = await (await request.get('/rest/api/3/myself?expand=groups,applicationRoles', { headers: headers() })).json();
  expect(me.accountId).toBeTruthy();
  expect(me.self).toContain(`/rest/api/3/user?accountId=${me.accountId}`);
  expect(me.groups).toHaveProperty('size');

  const original = (await (await request.get('/rest/api/3/mypreferences/locale', { headers: headers() })).json()).locale;
  expect((await request.put('/rest/api/3/mypreferences/locale', { headers: headers(), data: { locale: 'xx_XX' } })).status()).toBe(400);
  expect((await request.put('/rest/api/3/mypreferences/locale', { headers: headers(), data: { locale: 'fr_FR' } })).status()).toBe(204);
  expect((await (await request.get('/rest/api/3/myself', { headers: headers() })).json()).locale).toBe('fr_FR');
  await request.put('/rest/api/3/mypreferences/locale', { headers: headers(), data: { locale: original } });

  const found = await (await request.get(`/rest/api/3/user/search?query=${encodeURIComponent(DEMO.email)}`, { headers: headers() })).json();
  expect(found.map((u: { accountId: string }) => u.accountId)).toContain(me.accountId);

  const reported = await request.get(`/rest/api/3/user/search/query?query=${encodeURIComponent('is reporter of ZZ OR is assignee of ZZ')}`, { headers: headers() });
  expect(reported.status()).toBe(200);
  expect((await reported.json())).toHaveProperty('values');
  expect((await request.get(`/rest/api/3/user/search/query?query=${encodeURIComponent('is owner of ZZ')}`, { headers: headers() })).status()).toBe(400);

  const stamp = Date.now();
  const developers = await request.post('/rest/api/3/group', { headers: headers(), data: { name: `people-e2e-dev-${stamp}` } });
  expect(developers.status()).toBe(201);
  const operators = await request.post('/rest/api/3/group', { headers: headers(), data: { name: `people-e2e-ops-${stamp}` } });
  expect(operators.status()).toBe(201);
  const developersId = (await developers.json()).groupId;
  const operatorsId = (await operators.json()).groupId;
  expect((await request.post(`/rest/api/3/group/user?groupId=${developersId}`, { headers: headers(), data: { accountId: me.accountId } })).status()).toBe(201);

  const picker = await (await request.get(`/rest/api/3/groupuserpicker?query=people-e2e-dev-${stamp}`, { headers: headers() })).json();
  expect(picker.groups.total).toBe(1);

  expect((await request.delete(`/rest/api/3/group?groupId=${developersId}&swapGroupId=${operatorsId}`, { headers: headers() })).status()).toBe(200);
  expect((await request.get(`/rest/api/3/group?groupId=${developersId}`, { headers: headers() })).status()).toBe(404);
  const members = await (await request.get(`/rest/api/3/group/member?groupId=${operatorsId}`, { headers: headers() })).json();
  expect(members.values.map((u: { accountId: string }) => u.accountId)).toEqual([me.accountId]);
  expect((await request.delete(`/rest/api/3/group?groupId=${operatorsId}`, { headers: headers() })).status()).toBe(200);
});
