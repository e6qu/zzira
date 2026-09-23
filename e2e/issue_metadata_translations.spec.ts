import { expect, test, Page } from '@playwright/test';
import axe from 'axe-core';
import * as fs from 'fs';
import * as path from 'path';

const DEMO = { email: 'demo@zzira.dev', password: 'demo1234' };

function apiAuthHeader(): string {
  const tokens = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'seed-tokens.json'), 'utf8'));
  return 'Basic ' + Buffer.from(`${DEMO.email}:${tokens[DEMO.email]}`).toString('base64');
}

async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#login-email', DEMO.email);
  await page.fill('#login-password', DEMO.password);
  await page.click('button[type=submit]');
}

async function accessible(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (await (window as any).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] } })).violations);
  expect(violations).toEqual([]);
}

// An administrator names a work type, a priority and a resolution in another
// language, and a person who reads in that language is answered in it. The
// site's own name is what the API still takes and what JQL still searches.
test('the words on a work item are named in each language a site speaks', async ({ page }) => {
  await login(page);
  const auth = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36);

  // A work type of this site, so the journey never renames a shared one.
  const created = await page.request.post('/rest/api/3/issuetype', {
    headers: auth, data: { name: `Chore ${stamp}`, description: 'Work that has to happen.', type: 'standard' },
  });
  expect(created.status(), await created.text()).toBe(201);
  const workTypeID = (await created.json()).id as string;
  const workTypeName = `Chore ${stamp}`;
  const spanishName = `Tarea rutinaria ${stamp}`;

  const workTypeBean = async () => {
    const types = await (await page.request.get('/rest/api/3/issuetype', { headers: auth })).json() as Array<Record<string, string>>;
    return types.find(candidate => candidate.id === workTypeID)!;
  };
  expect((await workTypeBean()).name).toBe(workTypeName);
  expect((await workTypeBean()).translatedName).toBeUndefined();

  await page.goto('/settings/work-types');
  // Filtered by the card's own heading: every other card's delete form lists
  // this work type as somewhere to move work to, so matching on text alone
  // would match all of them.
  const chore = (): ReturnType<Page['locator']> =>
    page.locator('.metadata-card').filter({ has: page.getByRole('heading', { name: workTypeName, exact: true }) });
  const card = chore();
  const translations = card.locator('.metadata-translations');
  await translations.locator('summary').click();
  await translations.getByLabel('Language', { exact: true }).fill('es');
  await translations.getByLabel('Name in that language').fill(spanishName);
  await translations.getByLabel('Description in that language').fill('Trabajo que hay que hacer.');
  await accessible(page);
  await translations.getByRole('button', { name: 'Save translation' }).click();
  await expect(page.getByRole('status')).toContainText('Translation saved.');
  await expect(chore().locator('.metadata-translations')).toContainText(spanishName);

  // A language tag that is not one is refused, and says what one looks like.
  const again = chore().locator('.metadata-translations');
  await again.locator('summary').click();
  await again.getByLabel('Language', { exact: true }).fill('Espanol!');
  await again.getByLabel('Name in that language').fill('x');
  await again.getByRole('button', { name: 'Save translation' }).click();
  await expect(page.getByRole('alert')).toContainText('language tag');

  // Reading in Spanish: the work type resource answers in it, beside the
  // site's own name, which is what everything else still speaks.
  expect((await page.request.put('/rest/api/3/mypreferences/locale', { headers: auth, data: { locale: 'es_ES' } })).status()).toBe(204);
  expect((await workTypeBean()).translatedName).toBe(spanishName);
  expect((await workTypeBean()).name).toBe(workTypeName);
  expect((await workTypeBean()).translatedDescription).toBe('Trabajo que hay que hacer.');

  // A priority and a resolution translate the same way, from their own pages.
  await page.goto('/settings/priorities');
  const priority = page.locator('.metadata-card').first();
  const priorityName = (await priority.locator('h2').first().innerText()).trim();
  const priorityTranslations = priority.locator('.metadata-translations');
  await priorityTranslations.locator('summary').click();
  await priorityTranslations.getByLabel('Language', { exact: true }).fill('es');
  await priorityTranslations.getByLabel('Name in that language').fill(`Prioridad ${stamp}`);
  await priorityTranslations.getByRole('button', { name: 'Save translation' }).click();
  await expect(page.getByRole('status')).toContainText('Translation saved.');
  const priorities = await (await page.request.get('/rest/api/3/priority', { headers: auth })).json() as Array<Record<string, string>>;
  const translatedPriority = priorities.find(candidate => candidate.name === priorityName)!;
  expect(translatedPriority.translatedName).toBe(`Prioridad ${stamp}`);

  await page.goto('/settings/resolutions');
  const resolution = page.locator('.metadata-card').first();
  const resolutionName = (await resolution.locator('h2').first().innerText()).trim();
  const resolutionTranslations = resolution.locator('.metadata-translations');
  await resolutionTranslations.locator('summary').click();
  await resolutionTranslations.getByLabel('Language', { exact: true }).fill('es');
  await resolutionTranslations.getByLabel('Name in that language').fill(`Resolución ${stamp}`);
  await resolutionTranslations.getByRole('button', { name: 'Save translation' }).click();
  await expect(page.getByRole('status')).toContainText('Translation saved.');
  const resolutions = await (await page.request.get('/rest/api/3/resolution', { headers: auth })).json() as Array<Record<string, string>>;
  expect(resolutions.find(candidate => candidate.name === resolutionName)!.translatedName).toBe(`Resolución ${stamp}`);

  // Removing a translation takes that language back to the site's own name.
  await page.goto('/settings/work-types');
  const remaining = chore().locator('.metadata-translations');
  await remaining.locator('summary').click();
  await remaining.getByRole('button', { name: /Remove/ }).first().click();
  await expect(page.getByRole('status')).toContainText('Translation removed.');
  expect((await workTypeBean()).translatedName).toBeUndefined();

  expect((await page.request.put('/rest/api/3/mypreferences/locale', { headers: auth, data: { locale: 'en_US' } })).status()).toBe(204);
  for (const path of ['/settings/priorities', '/settings/resolutions']) {
    await page.goto(path);
    const card = page.locator('.metadata-card').first().locator('.metadata-translations');
    await card.locator('summary').click();
    await card.getByRole('button', { name: /Remove/ }).first().click();
    await expect(page.getByRole('status')).toContainText('Translation removed.');
  }
  expect((await page.request.delete(`/rest/api/3/issuetype/${workTypeID}`, { headers: auth })).status()).toBe(204);
});

// A person chooses a language on their profile, and the work item reads in it:
// the words the site chose are theirs, while everything they send is still the
// site's own.
test('a person reads a work item in the language they chose', async ({ page }) => {
  await login(page);
  const auth = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36);
  const frenchStatus = `À faire ${stamp}`;

  const created = await page.request.post('/rest/api/3/issue', {
    headers: auth,
    data: { fields: { project: { key: 'ZZ' }, summary: `Lu en français ${stamp}`, issuetype: { name: 'Task' } } },
  });
  expect(created.status(), await created.text()).toBe(201);
  const key = (await created.json()).key as string;
  const issue = await (await page.request.get(`/rest/api/3/issue/${key}`, { headers: auth })).json();
  const statusName = issue.fields.status.name as string;

  // A status is named in French where every other piece of metadata is.
  await page.goto('/settings/statuses');
  const status = page.locator('.status-directory-item').filter({ has: page.getByRole('heading', { name: statusName, exact: true }) }).first();
  const statusTranslations = status.locator('.metadata-translations');
  await statusTranslations.locator('summary').click();
  await statusTranslations.getByLabel('Language', { exact: true }).fill('fr');
  await statusTranslations.getByLabel('Name in that language').fill(frenchStatus);
  await statusTranslations.getByRole('button', { name: /Save translation/ }).click();
  await expect(page.getByRole('status')).toContainText('Translation saved.');

  // Before anybody chooses French, the page reads as the site named it.
  await page.goto(`/browse/${key}`);
  await expect(page.locator('main')).toContainText(statusName);
  await expect(page.locator('main')).not.toContainText(frenchStatus);

  await page.goto('/profile');
  const language = page.locator('.profile-language');
  await expect(language).toBeVisible();
  await language.getByLabel('Language').selectOption('fr');
  await language.getByRole('button', { name: 'Save language' }).click();
  await expect(page.getByRole('status')).toContainText('Reading the site in fr');
  await accessible(page);

  await page.goto(`/browse/${key}`);
  await expect(page.locator('main')).toContainText(frenchStatus);
  // The site's own word is what a search still takes.
  const found = await page.request.get(`/rest/api/3/search/jql?jql=${encodeURIComponent(`key = ${key} AND status = "${statusName}"`)}`, { headers: auth });
  expect(found.status(), await found.text()).toBe(200);
  expect((await found.json()).issues).toHaveLength(1);

  // The results and the filter above them read in it too. A board shows the
  // status as its column, which the board administrator named, so there is
  // nothing of the site's own words on it to translate.
  await page.goto('/issues/ZZ');
  await expect(page.locator('main')).toContainText(frenchStatus);
  await expect(page.getByLabel('Status').locator('option', { hasText: frenchStatus })).toHaveCount(1);

  // Back to the site's own language, and the translation is taken away.
  await page.goto('/profile');
  await page.locator('.profile-language').getByLabel('Language').selectOption('');
  await page.locator('.profile-language').getByRole('button', { name: 'Save language' }).click();
  await expect(page.getByRole('status')).toContainText("Reading the site in its own language");
  await page.goto('/settings/statuses');
  const again = page.locator('.status-directory-item').filter({ has: page.getByRole('heading', { name: statusName, exact: true }) }).first().locator('.metadata-translations');
  await again.locator('summary').click();
  await again.getByRole('button', { name: /Remove/ }).first().click();
  await expect(page.getByRole('status')).toContainText('Translation removed.');
  await page.goto(`/browse/${key}`);
  await expect(page.locator('main')).toContainText(statusName);
});

