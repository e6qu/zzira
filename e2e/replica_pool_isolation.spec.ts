import { apiAuthHeader } from './auth';
import { expect, test } from '@playwright/test';

test.use({ actionTimeout: 15_000 });

async function login(page: import('@playwright/test').Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill('demo@zzira.dev');
  await page.getByLabel('Password').fill('demo1234');
  await page.getByRole('button', { name: 'Log in' }).click();
}

async function create(page: import('@playwright/test').Page, summary: string) {
  const response = await page.request.post('/rest/api/3/issue', { headers: { Authorization: apiAuthHeader() }, data: { fields: { project: { key: 'ZZ' }, issuetype: { name: 'Task' }, summary } } });
  expect(response.status()).toBe(201);
  return String((await response.json()).key);
}

test('two tabs own independent pools and both drain offline edits', async ({ page, context }) => {
  await context.addInitScript(() => {
    const NativeWorker = window.Worker;
    (window as any).__refuseOfflineQueue = true;
    (window as any).__offlineAckDelayed = false;
    (window as any).Worker = class extends NativeWorker {
      postMessage(message: any, ...rest: any[]) {
        if (message?.type === 'enqueue' && message.requestId && (window as any).__refuseOfflineQueue) {
          (window as any).__refuseOfflineQueue = false;
          queueMicrotask(() => this.dispatchEvent(new MessageEvent('message', { data: { type: 'queue-error', requestId: message.requestId, message: 'Injected queue refusal' } })));
          return;
        }
        super.postMessage(message, ...rest);
      }
      set onmessage(handler: any) {
        super.onmessage = event => {
          if (event.data?.type === 'queued' && event.data.requestId) {
            (window as any).__offlineAckDelayed = true;
            setTimeout(() => { (window as any).__offlineAckDelayed = false; handler.call(this, event); }, 600);
          } else handler.call(this, event);
        };
      }
    };
  });
  await login(page);
  const stamp = Date.now();
  const keys = [await create(page, `First replica ${stamp}`), await create(page, `Second replica ${stamp}`)];
  const second = await context.newPage();
  const pages = [page, second];
  try {
    for (let i = 0; i < pages.length; i++) {
      await pages[i].goto(`/browse/${keys[i]}`);
      await expect(pages[i].locator('#sync-banner')).toContainText('synced', { timeout: 20_000 });
    }
    const ids = await Promise.all(pages.map(p => p.evaluate(() => sessionStorage.getItem('zzira-replica-id'))));
    expect(ids[0]).not.toBe(ids[1]);
    await context.setOffline(true);
    for (let i = 0; i < pages.length; i++) {
      await pages[i].locator('#edit-issue-button').click();
      await pages[i].locator('#edit-summary').fill('');
      await pages[i].getByRole('button', { name: 'Save changes', exact: true }).click();
      await expect(pages[i].getByRole('dialog', { name: 'Edit issue', exact: true })).toBeVisible();
      expect(await pages[i].locator('#edit-summary').evaluate(input => (input as HTMLInputElement).validity.valueMissing)).toBe(true);
      await pages[i].locator('#edit-summary').fill(`Queued replica ${i} ${stamp}`);
      const save = pages[i].getByRole('button', { name: 'Save changes', exact: true });
      await save.click();
      await expect(pages[i].locator('[data-offline-save-error]')).toHaveText('Injected queue refusal');
      await expect(pages[i].locator('#edit-summary')).toHaveValue(`Queued replica ${i} ${stamp}`);
      await expect(save).toBeEnabled();
      await save.click();
      await expect.poll(() => pages[i].evaluate(() => (window as any).__offlineAckDelayed)).toBe(true);
      await expect(pages[i].getByRole('dialog', { name: 'Edit issue', exact: true })).toBeVisible();
      await expect(save).toBeDisabled();
      await pages[i].keyboard.press('Escape');
      await expect(pages[i].getByRole('dialog', { name: 'Edit issue', exact: true })).toBeVisible();
      await expect(pages[i].getByRole('dialog', { name: 'Edit issue', exact: true })).not.toBeVisible();
      await pages[i].reload();
      await expect(pages[i].locator('#sync-banner')).toContainText('offline', { timeout: 20_000 });
      await expect(pages[i].locator('.issue-summary')).toHaveText(`Queued replica ${i} ${stamp}`);
    }
    await context.setOffline(false);
    for (let i = 0; i < keys.length; i++) {
      await expect.poll(async () => (await (await page.request.get(`/rest/api/3/issue/${keys[i]}?fields=summary`, { headers: { Authorization: apiAuthHeader() } })).json()).fields.summary, { timeout: 20_000 }).toBe(`Queued replica ${i} ${stamp}`);
    }
  } finally {
    await context.setOffline(false);
    await second.close();
    for (const key of keys) await page.request.delete(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() }, timeout: 10_000 });
  }
});

test('legacy migration preserves an outbox after a failed pool install', async ({ page, context }) => {
  await login(page);
  const summary = `Legacy replica ${Date.now()}`;
  const queuedSummary = `${summary} queued`;
  const key = await create(page, summary);
  try {
    await page.goto(`/browse/${key}`);
    await expect(page.locator('#sync-banner')).toContainText('synced', { timeout: 20_000 });
    const replica = await page.evaluate(() => sessionStorage.getItem('zzira-replica-id'));
    await page.goto('/projects'); // Stop the Go worker before the storage fixture acquires its handles.
    const migratedFixture = await page.evaluate(async ({ replica, key, queuedSummary }) => {
      const sqliteURL = new URL('/static/sqlite/sqlite3.js?v=3', location.href).href;
      const storageURL = new URL('/static/replica-storage.js?v=1', location.href).href;
      const origin = location.origin;
      const source = `
        importScripts(${JSON.stringify(sqliteURL)}, ${JSON.stringify(storageURL)});
        self.onmessage = async ({data}) => {
          try {
            const sqlite3 = await sqlite3InitModule({locateFile:name => new URL('/static/sqlite/' + name,data.origin).href});
            const filename = '/zzira-' + data.replica + '.db';
            const current = await self.zziraReplicaStorage(sqlite3,data.replica);
            const db = new current.OpfsSAHPoolDb(filename);
            db.exec({sql:'INSERT INTO outbox(method,path,body) VALUES(?,?,?)',bind:['POST','/issues/' + data.key + '/edit',new URLSearchParams({summary:data.queuedSummary}).toString()]});
            db.close();
            const bytes = current.exportFile(filename);
            const legacy = await sqlite3.installOpfsSAHPoolVfs();
            await legacy.reserveMinimumCapacity(legacy.getFileCount()+2);
            legacy.importDb(filename,bytes);
            legacy.pauseVfs();
            await current.removeVfs(); // Fixture only: force the application to migrate its legacy copy.
            const original = FileSystemFileHandle.prototype.createSyncAccessHandle;
            let calls = 0;
            FileSystemFileHandle.prototype.createSyncAccessHandle = function(...args) {
              const call = ++calls;
              if (call === 3) return Promise.reject(new Error('injected pool handle failure'));
              const acquired = original.apply(this,args);
              // Keep successful acquisitions pending after the injected
              // rejection, so cleanup must wait for all native handles.
              return call > 1 ? acquired.then(handle => new Promise(resolve => setTimeout(() => resolve(handle),25))) : acquired;
            };
            let refused = false;
            try { await sqlite3.installOpfsSAHPoolVfs({name:'legacy-failure-test',directory:'.opfs-sahpool'}); } catch (error) { refused = String(error).includes('injected pool handle failure'); }
            FileSystemFileHandle.prototype.createSyncAccessHandle = original;
            const check = await sqlite3.installOpfsSAHPoolVfs({name:'legacy-failure-test',directory:'.opfs-sahpool',forceReinitIfPreviouslyFailed:true});
            const retained = check.getFileNames().includes(filename);
            check.pauseVfs();
            self.postMessage({refused,retained});
          } catch (error) { self.postMessage({error:String(error)}); }
        };
      `;
      const blob = URL.createObjectURL(new Blob([source], { type: 'text/javascript' }));
      const worker = new Worker(blob);
      try {
        return await new Promise<any>((resolve, reject) => {
          const timeout = setTimeout(() => reject(new Error('storage fixture timed out')), 20_000);
          worker.onmessage = event => { clearTimeout(timeout); resolve(event.data); };
          worker.onerror = () => { clearTimeout(timeout); reject(new Error('storage fixture worker failed')); };
          worker.postMessage({ replica, key, queuedSummary, origin });
        });
      } finally { worker.terminate(); URL.revokeObjectURL(blob); }
    }, { replica, key, queuedSummary });
    expect(migratedFixture).toEqual({ refused: true, retained: true });
    await context.setOffline(true);
    await page.goto(`/browse/${key}`);
    await expect(page.locator('#sync-banner')).toContainText('offline', { timeout: 20_000 });
    await page.locator('#edit-issue-button').click();
    await expect(page.locator('#edit-summary')).toHaveValue(summary);
    await page.keyboard.press('Escape');
    await context.setOffline(false);
    await expect.poll(async () => (await (await page.request.get(`/rest/api/3/issue/${key}?fields=summary`, { headers: { Authorization: apiAuthHeader() } })).json()).fields.summary, { timeout: 20_000 }).toBe(queuedSummary);
  } finally {
    await context.setOffline(false);
    await page.request.delete(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() }, timeout: 10_000 });
  }
});


async function replicaStatement(page: import('@playwright/test').Page, replica: string, sql: string) {
  return page.evaluate(async ({ replica, sql }) => {
    const sqliteURL = new URL('/static/sqlite/sqlite3.js?v=3', location.href).href;
    const storageURL = new URL('/static/replica-storage.js?v=1', location.href).href;
    const source = `
      importScripts(${JSON.stringify(sqliteURL)}, ${JSON.stringify(storageURL)});
      self.onmessage = async ({data}) => {
        try {
          const sqlite3 = await sqlite3InitModule({locateFile:name => new URL('/static/sqlite/' + name,data.origin).href});
          const pool = await self.zziraReplicaStorage(sqlite3,data.replica);
          const db = new pool.OpfsSAHPoolDb('/zzira-' + data.replica + '.db');
          let outbox;
          try { db.exec(data.sql); outbox = db.selectValue('SELECT COUNT(*) FROM outbox'); }
          finally { db.close(); pool.pauseVfs(); }
          self.postMessage({outbox});
        } catch (error) { self.postMessage({error:String(error)}); }
      };
    `;
    const blob = URL.createObjectURL(new Blob([source], { type: 'text/javascript' }));
    const worker = new Worker(blob);
    try {
      return await new Promise<any>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error('replica statement timed out')), 20_000);
        worker.onmessage = event => { clearTimeout(timer); resolve(event.data); };
        worker.onerror = () => { clearTimeout(timer); reject(new Error('replica statement worker failed')); };
        worker.postMessage({ replica, sql, origin: location.origin });
      });
    } finally { worker.terminate(); URL.revokeObjectURL(blob); }
  }, { replica, sql });
}

test('a refused optimistic update rolls back its outbox and retains the draft', async ({ page, context }) => {
  await login(page);
  const summary = `Atomic offline edit ${Date.now()}`;
  const changed = `${summary} changed`;
  const key = await create(page, summary);
  try {
    await page.goto(`/browse/${key}`);
    await expect(page.locator('#sync-banner')).toContainText('synced', { timeout: 20_000 });
    const replica = await page.evaluate(() => sessionStorage.getItem('zzira-replica-id'));
    expect(replica).toBeTruthy();
    await page.goto('/projects');
    expect(await replicaStatement(page, replica!, "CREATE TRIGGER reject_local_edit BEFORE UPDATE ON issues BEGIN SELECT RAISE(ABORT,'injected optimistic update refusal'); END")).toEqual({ outbox: 0 });
    await context.setOffline(true);
    await page.goto(`/browse/${key}`);
    await expect(page.locator('#sync-banner')).toContainText('offline', { timeout: 20_000 });
    await page.locator('#edit-issue-button').click();
    await page.locator('#edit-summary').fill(changed);
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(page.locator('[data-offline-save-error]')).toHaveText('The change could not be saved offline.');
    await expect(page.locator('#edit-summary')).toHaveValue(changed);
    await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeEnabled();
    await page.keyboard.press('Escape');
    await page.goto('/projects');
    expect(await replicaStatement(page, replica!, 'DROP TRIGGER reject_local_edit')).toEqual({ outbox: 0 });
    await page.goto(`/browse/${key}`);
    await expect(page.locator('.issue-summary')).toHaveText(summary);
    await page.locator('#edit-issue-button').click();
    await page.locator('#edit-summary').fill(changed);
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Edit issue', exact: true })).not.toBeVisible();
    await page.reload();
    await expect(page.locator('.issue-summary')).toHaveText(changed);
    await context.setOffline(false);
    await expect.poll(async () => (await (await page.request.get(`/rest/api/3/issue/${key}?fields=summary`, { headers: { Authorization: apiAuthHeader() } })).json()).fields.summary, { timeout: 20_000 }).toBe(changed);
  } finally {
    await context.setOffline(false);
    await page.request.delete(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() }, timeout: 10_000 });
  }
});

test('offline description clearing survives reload while omitted fields retain their value', async ({ page, context }) => {
  await login(page);
  const summary = `Offline description ${Date.now()}`;
  const description = `${summary} original text`;
  const response = await page.request.post('/rest/api/3/issue', {
    headers: { Authorization: apiAuthHeader() },
    data: { fields: { project: { key: 'ZZ' }, issuetype: { name: 'Task' }, summary,
      description: { type: 'doc', version: 1, content: [{ type: 'paragraph', content: [{ type: 'text', text: description, marks: [{ type: 'strong' }] }] }] } } },
  });
  expect(response.status()).toBe(201);
  const key = String((await response.json()).key);
  try {
    await page.goto(`/browse/${key}`);
    await expect(page.locator('#sync-banner')).toContainText('synced', { timeout: 20_000 });
    await expect(page.locator('.issue-description .description-text')).toHaveText(description);
    await context.setOffline(true);
    await page.locator('#edit-issue-button').click();
    await expect(page.locator('#edit-description')).toHaveValue(description);
    // An omitted field leaves its replica value alone.
    await page.locator('#edit-description').evaluate(field => field.remove());
    await page.locator('#edit-summary').fill(`${summary} changed`);
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Edit issue', exact: true })).not.toBeVisible();
    await page.reload();
    await expect(page.locator('.issue-description strong')).toHaveText(description);
    // An unchanged value submitted by the ordinary form preserves rich formatting.
    await page.locator('#edit-issue-button').click();
    await expect(page.locator('#edit-description')).toHaveValue(description);
    await page.locator('#edit-summary').fill(`${summary} unchanged description`);
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Edit issue', exact: true })).not.toBeVisible();
    await page.reload();
    await expect(page.locator('.issue-description strong')).toHaveText(description);
    // A present empty value is a clear, and must survive an offline reload.
    await page.locator('#edit-issue-button').click();
    await expect(page.locator('#edit-description')).toHaveValue(description);
    await page.locator('#edit-description').fill('');
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Edit issue', exact: true })).not.toBeVisible();
    await page.reload();
    await expect(page.locator('.issue-description .description-text')).toHaveText('');
    await page.locator('#edit-issue-button').click();
    await expect(page.locator('#edit-description')).toHaveValue('');
    await page.keyboard.press('Escape');
    await context.setOffline(false);
    await expect.poll(async () => {
      const fetched = await page.request.get(`/rest/api/3/issue/${key}?fields=summary,description`, { headers: { Authorization: apiAuthHeader() } });
      expect(fetched.status()).toBe(200);
      const fields = (await fetched.json()).fields;
      const text = (node: any): string => String(node?.text ?? '') + (node?.content ?? []).map(text).join('');
      return { summary: fields.summary, description: text(fields.description) };
    }, { timeout: 20_000 }).toEqual({ summary: `${summary} unchanged description`, description: '' });
  } finally {
    await context.setOffline(false);
    await page.request.delete(`/rest/api/3/issue/${key}`, { headers: { Authorization: apiAuthHeader() }, timeout: 10_000 });
  }
});
