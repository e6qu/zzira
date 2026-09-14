import { expect, test } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

// A Jira Software client on the seeded site: an epic and its story, ranking,
// a board created from a filter with story point estimation, feature flag data
// on the story and a Jira expression over the result.

const DEMO = { email: 'demo@zzira.dev' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

test('Jira Software client works with epics, ranking, estimation, DevOps data and expressions', async ({ request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const stamp = Date.now();
  const createIssue = async (fields: Record<string, unknown>) => {
    const response = await request.post('/rest/api/3/issue', { headers, data: { fields } });
    expect(response.status(), await response.text()).toBe(201);
    return (await response.json()).key as string;
  };
  const epic = await createIssue({ project: { key: 'ZZ' }, summary: `Launch ${stamp}`, issuetype: { name: 'Epic' } });
  const story = await createIssue({ project: { key: 'ZZ' }, summary: `Sign-up ${stamp}`, issuetype: { name: 'Story' }, parent: { key: epic } });

  // The epic and its story.
  const renamed = await request.post(`/rest/agile/1.0/epic/${epic}`, { headers, data: { name: 'Launch', color: { key: 'color_3' } } });
  expect(renamed.status(), await renamed.text()).toBe(200);
  expect(await renamed.json()).toMatchObject({ key: epic, name: 'Launch', color: { key: 'color_3' }, done: false });
  const children = await (await request.get(`/rest/agile/1.0/epic/${epic}/issue`, { headers })).json();
  expect(children.issues.map((issue: { key: string }) => issue.key)).toEqual([story]);
  const agileIssue = await (await request.get(`/rest/agile/1.0/issue/${story}`, { headers })).json();
  expect(agileIssue.fields.epic).toMatchObject({ key: epic, name: 'Launch' });

  // Site-wide ranking.
  const ranked = await request.put('/rest/agile/1.0/issue/rank', { headers, data: { issues: [story], rankBeforeIssue: epic } });
  expect(ranked.status(), await ranked.text()).toBe(204);
  const partial = await request.put('/rest/agile/1.0/issue/rank', { headers, data: { issues: [story, 'NOPE-404'], rankAfterIssue: epic } });
  expect(partial.status()).toBe(207);
  expect((await partial.json()).entries.map((entry: { status: number }) => entry.status)).toEqual([200, 404]);

  // A scrum board from the seeded board's filter estimates with story points.
  const boards = await (await request.get('/rest/agile/1.0/board', { headers })).json();
  const configuration = await (await request.get(`/rest/agile/1.0/board/${boards.values[0].id}/configuration`, { headers })).json();
  const created = await request.post('/rest/agile/1.0/board', {
    headers,
    data: { name: `Scrum ${stamp}`, type: 'scrum', filterId: Number(configuration.filter.id), location: { type: 'project', projectKeyOrId: 'ZZ' } },
  });
  expect(created.status(), await created.text()).toBe(201);
  const board = await created.json();
  const boardConfiguration = await (await request.get(`/rest/agile/1.0/board/${board.id}/configuration`, { headers })).json();
  expect(boardConfiguration.estimation).toMatchObject({ type: 'field', field: { displayName: 'Story point estimate' } });
  const estimated = await request.put(`/rest/agile/1.0/issue/${story}/estimation?boardId=${board.id}`, { headers, data: { value: '8' } });
  expect(estimated.status(), await estimated.text()).toBe(200);
  expect((await estimated.json()).value).toBe(8);

  // Feature flag data associated with the story.
  const flags = await request.post('/rest/featureflags/0.1/bulk', {
    headers,
    data: {
      properties: { source: `e2e-${stamp}` },
      flags: [{
        schemaVersion: '1.0', id: `flag-${stamp}`, key: 'signup-v2', updateSequenceId: 1, displayName: 'Sign-up v2',
        associations: [{ associationType: 'issueIdOrKeys', values: [story] }],
        summary: { status: { enabled: true }, lastUpdated: new Date().toISOString() },
        details: [{ url: 'https://flags.example.com/signup-v2', lastUpdated: new Date().toISOString(), environment: { name: 'production', type: 'production' }, status: { enabled: true } }],
      }],
    },
  });
  expect(flags.status(), await flags.text()).toBe(202);
  expect((await flags.json()).acceptedFeatureFlags).toEqual([`flag-${stamp}`]);
  expect((await request.get(`/rest/featureflags/0.1/flag/flag-${stamp}`, { headers })).status()).toBe(200);
  expect((await request.delete(`/rest/featureflags/0.1/bulkByProperties?source=e2e-${stamp}`, { headers })).status()).toBe(202);

  // A Jira expression over the story.
  const evaluated = await request.post('/rest/api/3/expression/eval?expand=meta.complexity', {
    headers,
    data: { expression: '{ epic: issue.epic.key, stories: issue.epic.stories.map(s => s.key), type: issue.issueType.name }', context: { issue: { key: story } } },
  });
  expect(evaluated.status(), await evaluated.text()).toBe(200);
  const result = await evaluated.json();
  expect(result.value).toEqual({ epic, stories: [story], type: 'Story' });
  expect(result.meta.complexity.expensiveOperations.value).toBe(2);

  expect((await request.delete(`/rest/agile/1.0/board/${board.id}`, { headers })).status()).toBe(204);
});
