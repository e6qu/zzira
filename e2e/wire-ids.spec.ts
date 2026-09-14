import { expect, test, APIRequestContext } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

// Clients must only ever see the ids Jira, Jira Software and Jira Service
// Management use. This crawls the read APIs on a seeded site and fails on any
// stored id — st_…, prj_…, brd_… and the like — in an id, a list of ids, a
// status reference or a URL.

const DEMO = { email: 'demo@zzira.dev' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  const token = process.env.ZZIRA_API_TOKEN ?? tokens[DEMO.email];
  return 'Basic ' + Buffer.from(`${DEMO.email}:${token}`).toString('base64');
}

const STORED_ID = /^(st|prj|brd|spr|wf|workflow|flt|lnk|cmt|att|wl|iss|qf|status|project|board|sprint|filter|queue|scheme|task)_[A-Za-z0-9_]+$/;
const STORED_ID_IN_URL = /\/(st|prj|brd|spr|wf|workflow|flt|lnk|cmt|att|wl|iss|qf|status|project|board|sprint|filter)_[A-Za-z0-9]+/;

function leaks(node: unknown, where: string, out: string[]) {
  if (Array.isArray(node)) {
    node.forEach((child) => leaks(child, `${where}[]`, out));
  } else if (node && typeof node === 'object') {
    for (const [key, child] of Object.entries(node as Record<string, unknown>)) {
      leaks(child, `${where}.${key}`, out);
    }
  } else if (typeof node === 'string') {
    const key = where.split('.').pop()!.replace(/\[\]$/, '');
    const idLike = /id$|ids$/i.test(key) || key === 'self' || key === 'statusReference' || /url/i.test(key) || key === 'jiraRest';
    if ((idLike && STORED_ID.test(node)) || STORED_ID_IN_URL.test(node)) {
      out.push(`${where} = ${node}`);
    }
  }
}

async function crawl(request: APIRequestContext, paths: string[]) {
  const headers = { Authorization: apiAuthHeader() };
  const found: string[] = [];
  for (const endpoint of paths) {
    const response = await request.get(endpoint, { headers });
    if (response.status() >= 400) {
      continue;
    }
    const text = await response.text();
    if (!text.trim().startsWith('{') && !text.trim().startsWith('[')) {
      continue;
    }
    const out: string[] = [];
    leaks(JSON.parse(text), '$', out);
    found.push(...out.map((leak) => `${endpoint}: ${leak}`));
  }
  return found;
}

test('no stored id reaches a client through the Jira, Agile or Service Management APIs', async ({ request }) => {
  const headers = { Authorization: apiAuthHeader() };
  const created = await request.post('/rest/api/3/issue', {
    headers,
    data: { fields: { project: { key: 'ZZ' }, summary: `Wire id audit ${Date.now()}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status()).toBe(201);
  const issueKey = (await created.json()).key;
  await request.post(`/rest/api/3/issue/${issueKey}/comment`, {
    headers,
    data: { body: { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: 'audit' }] }] } },
  });

  const project = await (await request.get('/rest/api/3/project/ZZ', { headers })).json();
  const boards = await (await request.get('/rest/agile/1.0/board', { headers })).json();
  const boardId = boards.values?.[0]?.id;
  const sprints = boardId ? await (await request.get(`/rest/agile/1.0/board/${boardId}/sprint`, { headers })).json() : { values: [] };
  const sprintId = sprints.values?.[0]?.id;
  const serviceDesks = await (await request.get('/rest/servicedeskapi/servicedesk', { headers })).json();
  const serviceDeskId = serviceDesks.values?.[0]?.id;
  const workflows = await (await request.get('/rest/api/3/workflow/search', { headers })).json();
  const workflowId = workflows.values?.[0]?.id;
  const schemes = await (await request.get('/rest/api/3/workflowscheme', { headers })).json();
  const schemeId = schemes.values?.[0]?.id;

  const paths = [
    '/rest/api/3/project', '/rest/api/3/project/ZZ', '/rest/api/3/project/search', `/rest/api/3/project/${project.id}`,
    '/rest/api/3/status', '/rest/api/3/statuses/search', '/rest/api/3/statuscategory',
    '/rest/api/3/workflow/search', '/rest/api/3/workflowscheme', `/rest/api/3/workflowscheme/project?projectId=${project.id}`,
    `/rest/api/3/issue/${issueKey}?expand=transitions,changelog,editmeta,names`, `/rest/api/3/issue/${issueKey}/transitions?expand=transitions.fields`,
    `/rest/api/3/issue/${issueKey}/comment`, `/rest/api/3/issue/${issueKey}/changelog`,
    '/rest/api/3/search/jql?jql=project%3DZZ&fields=*all', '/rest/api/3/filter/search', '/rest/api/3/filter/my', '/rest/api/3/filter/favourite', '/rest/api/3/dashboard',
    '/rest/api/3/issuesecurityschemes', '/rest/api/3/permissionscheme', '/rest/api/3/notificationscheme',
    '/rest/api/3/project/ZZ/versions', '/rest/api/3/project/ZZ/components', '/rest/api/3/screens', '/rest/api/3/screenscheme',
    '/rest/api/3/issuetypescreenscheme', '/rest/api/3/fieldconfiguration', '/rest/api/3/fieldconfigurationscheme', '/rest/api/3/field',
    '/rest/api/3/role', '/rest/api/3/projectCategory', '/rest/api/3/issuetype', '/rest/api/3/priority', '/rest/api/3/resolution',
    '/rest/api/3/issue/createmeta?projectKeys=ZZ&expand=projects.issuetypes.fields', `/rest/api/3/issue/createmeta/ZZ/issuetypes`,
    '/rest/api/3/issueLinkType', '/rest/agile/1.0/board',
  ];
  if (boardId) {
    for (const suffix of ['', '/configuration', '/sprint', '/issue', '/backlog', '/project', '/quickfilter', '/features', '/epic', '/epic/none/issue']) {
      paths.push(`/rest/agile/1.0/board/${boardId}${suffix}`);
    }
  }
  if (workflowId) {
    paths.push(`/rest/api/3/workflow/${workflowId}/workflowSchemes`, `/rest/api/3/workflow/${workflowId}/projectUsages`);
  }
  if (schemeId) {
    paths.push(`/rest/api/3/workflowscheme/${schemeId}`, `/rest/api/3/workflowscheme/${schemeId}/projectUsages`);
  }
  paths.push('/rest/agile/1.0/epic/none/issue', `/rest/agile/1.0/issue/${issueKey}`, '/rest/software/1.0/epic/none/issue');
  if (sprintId) {
    paths.push(`/rest/agile/1.0/sprint/${sprintId}`, `/rest/agile/1.0/sprint/${sprintId}/issue`);
  }
  if (serviceDeskId) {
    paths.push('/rest/servicedeskapi/servicedesk', '/rest/servicedeskapi/request', '/rest/servicedeskapi/organization',
      `/rest/servicedeskapi/servicedesk/${serviceDeskId}/queue`, `/rest/servicedeskapi/servicedesk/${serviceDeskId}/requesttype`);
  }
  const found = await crawl(request, paths);
  expect(found, found.join('\n')).toEqual([]);
});
