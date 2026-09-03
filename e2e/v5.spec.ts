import { test, expect, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function authFor(email: string): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN && email === DEMO.email ? process.env.ZZIRA_API_TOKEN : tokens[email];
  return 'Basic ' + Buffer.from(`${email}:${token}`).toString('base64');
}

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', DEMO.email);
  await page.fill('input[name=password]', DEMO.password);
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

test('V5 done-when: security level hides an issue from ana (404 + tombstone in her sync)', async ({ request }) => {
  const demo = { headers: { Authorization: authFor(DEMO.email) } };
  const ana = { headers: { Authorization: authFor('ana@zzira.dev') } };

  const me = await request.get('/rest/api/3/myself', demo);
  const demoId = (await me.json()).accountId;
  const anaList = await (await request.get('/rest/api/3/user/search?query=ana', demo)).json();
  const anaId = anaList[0].accountId;

  const created = await request.post('/rest/api/3/issue', {
    ...demo,
    data: { fields: { project: { key: 'ZZ' }, summary: `V5 secret ${Date.now()}`, issuetype: { name: 'Task' } } },
  });
  const createdBody = await created.json();
  const key = createdBody.key;
  const issueId = createdBody.id;

  // ana can see it now
  expect((await request.get(`/rest/api/3/issue/${key}`, ana)).status()).toBe(200);

  // scheme: level restricted to demo
  await request.post('/rest/api/3/issuesecurityschemes', {
    ...demo,
    data: { id: 'sch_conf', name: 'Confidential Scheme', levels: [{ id: 'lvl_private', name: 'Private', members: [demoId] }] },
  });
  await request.put('/rest/api/3/issuesecurityschemes/project/ZZ', { ...demo, data: { id: 'sch_conf' } });

  // apply the level
  await request.put(`/rest/api/3/issue/${key}`, { ...demo, data: { fields: { security: { id: 'lvl_private' } } } });

  // demo 200, ana 404
  expect((await request.get(`/rest/api/3/issue/${key}`, demo)).status()).toBe(200);
  expect((await request.get(`/rest/api/3/issue/${key}`, ana)).status()).toBe(404);

  // ana's sync stream carries the per-user tombstone for THIS issue;
  // demo's stream never carries tombstones (members keep their replica).
  const tombstonesFor = async (auth: { headers: { Authorization: string } }) => {
    let since = 0;
    const actions: any[] = [];
    for (let page = 0; page < 100; page += 1) {
      const r = await request.get(`/sync?workspace=zzira&since=${since}&limit=1000`, auth);
      if (r.status() === 304) break;
      expect(r.status()).toBe(200);
      const body = await r.json();
      actions.push(...(body.actions ?? []));
      expect(body.to).toBeGreaterThan(since);
      since = body.to;
      if (!body.truncated) break;
    }
    return actions.filter((a: any) => a.entityType === 'tombstone' && JSON.stringify(a.payload).includes(`"${issueId}"`));
  };
  expect((await tombstonesFor(ana)).length).toBeGreaterThanOrEqual(1);
  expect((await tombstonesFor(demo)).length).toBe(0);
});
