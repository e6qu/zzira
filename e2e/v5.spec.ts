import { test, expect, APIRequestContext, Page } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function authFor(email: string): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN && email === DEMO.email ? process.env.ZZIRA_API_TOKEN : tokens[email];
  return 'Basic ' + Buffer.from(`${email}:${token}`).toString('base64');
}

async function loginAsAna(page: Page) {
  await page.goto('/login');
  await page.fill('input[name=email]', 'ana@zzira.dev');
  await page.fill('input[name=password]', 'ana12345');
  await page.click('button[type=submit]');
  await expect(page).toHaveURL('/');
}

// useSecurityLevel creates a scheme with one level only the account can see,
// associates it with the project through Jira's mapping task, and returns the
// level's id.
async function useSecurityLevel(request: APIRequestContext, auth: { headers: { Authorization: string } }, projectID: string, name: string, accountID: string): Promise<string> {
  const created = await request.post('/rest/api/3/issuesecurityschemes', {
    ...auth,
    data: { name, levels: [{ name: 'Private', members: [{ type: 'user', parameter: accountID }] }] },
  });
  expect(created.status()).toBe(201);
  const schemeID = String((await created.json()).id);
  const scheme = await (await request.get(`/rest/api/3/issuesecurityschemes/${schemeID}`, auth)).json();
  const levelID = String(scheme.levels[0].id);
  const current = await (await request.get(`/rest/api/3/issuesecurityschemes/project?projectId=${projectID}`, auth)).json();
  const mappings: { oldLevelId: string; newLevelId: string }[] = [];
  for (const association of current.values ?? []) {
    const previous = await (await request.get(`/rest/api/3/issuesecurityschemes/${association.issueSecuritySchemeId}`, auth)).json();
    for (const level of previous.levels ?? []) mappings.push({ oldLevelId: String(level.id), newLevelId: levelID });
  }
  const assigned = await request.put('/rest/api/3/issuesecurityschemes/project', {
    ...auth,
    maxRedirects: 0,
    data: { projectId: projectID, schemeId: schemeID, oldToNewSecurityLevelMappings: mappings },
  });
  expect(assigned.status()).toBe(303);
  await expect.poll(async () => String((await (await request.get(`/rest/api/3/issuesecurityschemes/project?projectId=${projectID}`, auth)).json()).values?.[0]?.issueSecuritySchemeId)).toBe(schemeID);
  return levelID;
}

test('V5 done-when: security level hides an issue from ana (404 + tombstone in her sync)', async ({ browser, request }) => {
  const demo = { headers: { Authorization: authFor(DEMO.email) } };
  const anaContext = await browser.newContext();
  const anaPage = await anaContext.newPage();
  await loginAsAna(anaPage);

  const me = await request.get('/rest/api/3/myself', demo);
  const demoId = (await me.json()).accountId;
  const syncActions = async (client: APIRequestContext, auth?: { headers: { Authorization: string } }) => {
    let since = 0;
    const actions: any[] = [];
    for (let page = 0; page < 100; page += 1) {
      const r = await client.get(`/sync?workspace=zzira&since=${since}&limit=1000`, auth);
      if (r.status() === 304) break;
      expect(r.status()).toBe(200);
      const body = await r.json();
      actions.push(...(body.actions ?? []));
      expect(body.to).toBeGreaterThan(since);
      since = body.to;
      if (!body.truncated) break;
    }
    return actions;
  };

  const created = await request.post('/rest/api/3/issue', {
    ...demo,
    data: { fields: { project: { key: 'ZZ' }, summary: `V5 secret ${Date.now()}`, issuetype: { name: 'Task' } } },
  });
  const createdBody = await created.json();
  const key = createdBody.key;

  // ana can see it now
  expect((await anaContext.request.get(`/rest/api/3/issue/${key}`)).status()).toBe(200);
  const visibleIssue = (await syncActions(anaContext.request)).find(
    (a: any) => a.entityType === 'issue' && a.payload?.issue?.key === key,
  );
  expect(visibleIssue).toBeTruthy();
  const reconciliationId = visibleIssue.payload.issue.id;

  // scheme: level restricted to demo
  const projectID = String((await (await request.get('/rest/api/3/project/ZZ', demo)).json()).id);
  const levelID = await useSecurityLevel(request, demo, projectID, `Confidential ${Date.now()}`, demoId);

  // apply the level
  await request.put(`/rest/api/3/issue/${key}`, { ...demo, data: { fields: { security: { id: levelID } } } });

  // demo 200, ana 404
  expect((await request.get(`/rest/api/3/issue/${key}`, demo)).status()).toBe(200);
  expect((await anaContext.request.get(`/rest/api/3/issue/${key}`)).status()).toBe(404);

  // ana's sync stream carries the per-user tombstone for THIS issue;
  // demo's stream never carries tombstones (members keep their replica).
  const tombstonesFor = async (client: APIRequestContext, auth?: { headers: { Authorization: string } }) => {
    const actions = await syncActions(client, auth);
    return actions.filter((a: any) =>
      a.entityType === 'tombstone' && a.payload?.issueId === reconciliationId,
    );
  };
  expect((await tombstonesFor(anaContext.request)).length).toBeGreaterThanOrEqual(1);
  expect((await tombstonesFor(request, demo)).length).toBe(0);
  await anaContext.close();
});
