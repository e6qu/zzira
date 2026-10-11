import { expect, type APIRequestContext } from '@playwright/test';

// Cloud REST keeps global contexts global. Fixture fields get a new scoped
// context before their unused global context is removed.
export async function scopeFixtureField(
  request: APIRequestContext, fieldID: string, globalID: string,
  projectID: string, headers: Record<string, string>,
): Promise<string> {
  const path = `/rest/api/3/field/${fieldID}/context`;
  const created = await request.post(path, {
    headers, data: { name: 'Fixture project', projectIds: [projectID] },
  });
  expect(created.status()).toBe(201);
  const contextID = String((await created.json()).id);
  expect((await request.delete(`${path}/${globalID}`, { headers })).status()).toBe(204);
  return contextID;
}
