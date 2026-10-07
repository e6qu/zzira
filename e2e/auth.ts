import * as fs from 'fs';
import * as path from 'path';

// An isolated instance may keep its credentials outside the working tree.
// Only the demo account uses the single-token override; other personas need
// their own seeded credentials for permission and notification journeys.
export function apiAuthHeader(email = 'demo@zzira.dev'): string {
  let token = email === 'demo@zzira.dev' ? process.env.ZZIRA_API_TOKEN : undefined;
  if (!token) {
    token = seedCredentials()[email];
  }
  if (!token) throw new Error(`No API token for ${email}: seed the instance or set ZZIRA_SEED_TOKENS.`);
  return 'Basic ' + Buffer.from(`${email}:${token}`).toString('base64');
}

export function seedPassword(email = 'demo@zzira.dev'): string {
  const password = seedCredentials()[`${email}.password`];
  if (!password) throw new Error(`No seeded password for ${email}: seed the instance or set ZZIRA_SEED_TOKENS.`);
  return password;
}

function seedCredentials(): Record<string, string> {
  const tokenFile = process.env.ZZIRA_SEED_TOKENS || path.join(__dirname, '..', 'data', 'seed-tokens.json');
  return JSON.parse(fs.readFileSync(tokenFile, 'utf8'));
}
