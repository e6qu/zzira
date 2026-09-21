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

// An administrator names a field in another language, and the people who read
// in that language see their own name for it -- on the form and over REST.
test('a field is named in each language a site speaks', async ({ page }) => {
  await login(page);
  const auth = { Authorization: apiAuthHeader() };
  const stamp = Date.now().toString(36);
  const projectKey = `TR${Date.now().toString().slice(-6)}`;
  const fieldName = `Release ring ${stamp}`;
  const spanishName = `Anillo de lanzamiento ${stamp}`;

  const me = await (await page.request.get('/rest/api/3/myself')).json();
  const created = await page.request.post('/rest/api/3/project', {
    headers: auth, data: { key: projectKey, name: `Translations ${stamp}`, projectTypeKey: 'software', leadAccountId: me.accountId, assigneeType: 'PROJECT_LEAD' },
  });
  expect(created.status()).toBe(201);
  const projectID = String((await created.json()).id);
  const field = await page.request.post('/rest/api/3/field', { headers: auth, data: { name: fieldName, type: 'text' } });
  expect(field.status()).toBe(201);
  const fieldID = (await field.json()).id as string;
  const contexts = await (await page.request.get(`/rest/api/3/field/${fieldID}/context`, { headers: auth })).json();
  expect((await page.request.put(`/rest/api/3/field/${fieldID}/context/${String(contexts.values[0].id)}/project`, {
    headers: auth, data: { projectIds: [projectID] },
  })).status()).toBe(204);

  // Until someone translates it, every language reads the site's own name.
  const fieldBean = async () => {
    const fields = await (await page.request.get('/rest/api/3/field', { headers: auth })).json() as Array<Record<string, string>>;
    return fields.find(candidate => candidate.id === fieldID)!;
  };
  expect((await fieldBean()).translatedName).toBe(fieldName);

  await page.goto('/settings/custom-fields');
  const card = page.locator(`.custom-field-card[data-field-id="${fieldID}"]`);
  const translations = card.locator('.custom-field-translations');
  await translations.locator('summary').click();
  await expect(translations).toContainText('No translations yet.');
  await translations.getByLabel('Language tag').fill('es');
  await translations.getByLabel('Name in that language').fill(spanishName);
  await translations.getByLabel('Description in that language').fill('El anillo en el que se publica el trabajo');
  await accessible(page);
  await translations.getByRole('button', { name: 'Save translation' }).click();
  await expect(page.getByRole('status')).toContainText('Field translation saved.');
  await expect(page.locator(`.custom-field-card[data-field-id="${fieldID}"] .custom-field-translations`)).toContainText(spanishName);

  // A language tag that is not one is refused, and says what one looks like.
  const badTranslations = page.locator(`.custom-field-card[data-field-id="${fieldID}"] .custom-field-translations`);
  await badTranslations.locator('summary').click();
  await badTranslations.getByLabel('Language tag').fill('Espanol!');
  await badTranslations.getByLabel('Name in that language').fill('x');
  await badTranslations.getByRole('button', { name: 'Save translation' }).click();
  await expect(page.getByRole('alert')).toContainText('language tag');

  // Reading in Spanish: the create form and the REST field both answer in it.
  expect((await page.request.put('/rest/api/3/mypreferences/locale', { headers: auth, data: { locale: 'es_ES' } })).status()).toBe(204);
  expect((await fieldBean()).translatedName).toBe(spanishName);
  expect((await fieldBean()).name).toBe(fieldName);

  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  const dialog = page.getByRole('dialog', { name: 'Create issue' });
  await expect(dialog).toBeVisible();
  await dialog.locator('.create-more summary').click();
  await expect(dialog.locator(`label[for="create-${fieldID}"]`)).toHaveText(new RegExp(spanishName));
  await page.keyboard.press('Escape');

  // Reading in English again: the site's own name.
  expect((await page.request.put('/rest/api/3/mypreferences/locale', { headers: auth, data: { locale: 'en_US' } })).status()).toBe(204);
  expect((await fieldBean()).translatedName).toBe(fieldName);
  await page.goto(`/projects/${projectKey}`);
  await page.locator('#global-create-issue').click();
  await expect(page.getByRole('dialog', { name: 'Create issue' })).toBeVisible();
  await page.locator('.create-more summary').click();
  await expect(page.locator(`label[for="create-${fieldID}"]`)).toHaveText(new RegExp(fieldName));
  await page.keyboard.press('Escape');

  // Removing the translation takes the language back to the site's name.
  await page.goto('/settings/custom-fields');
  const remaining = page.locator(`.custom-field-card[data-field-id="${fieldID}"] .custom-field-translations`);
  await remaining.locator('summary').click();
  await remaining.getByRole('button', { name: /Remove/ }).first().click();
  await expect(page.getByRole('status')).toContainText('Field translation removed.');
  expect((await page.request.put('/rest/api/3/mypreferences/locale', { headers: auth, data: { locale: 'es_ES' } })).status()).toBe(204);
  expect((await fieldBean()).translatedName).toBe(fieldName);
  expect((await page.request.put('/rest/api/3/mypreferences/locale', { headers: auth, data: { locale: 'en_US' } })).status()).toBe(204);

  expect((await page.request.delete(`/rest/api/3/project/${projectKey}?enableUndo=false`, { headers: auth })).status()).toBe(204);
});
