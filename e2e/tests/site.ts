import { test as base, expect, type Page, type TestInfo } from '@playwright/test';

export const port = process.env.E2E_PORT ?? '8090';
export const platformName = process.env.PLATFORM_NAME ?? 'meet.space';
export const groupName = 'Front Range Hikers';
export const hosts = {
  platform: `localhost:${port}`,
  group: `hikers.localhost:${port}`,
  origin: `origin.localhost:${port}`,
};

export function isCustomDomain(info: TestInfo): boolean {
  return info.project.name === 'custom-domain';
}

export function siteName(info: TestInfo): string {
  return isCustomDomain(info) ? groupName : platformName;
}

// Every page under test may only talk to its own host: anything else is
// aborted and recorded, so e2e never touches the network and a white-label
// page provably loads nothing from elsewhere.
export const test = base.extend<{ foreignRequests: string[] }>({
  foreignRequests: async ({ page, baseURL }, use) => {
    const own = new URL(baseURL!).host;
    const foreign: string[] = [];
    await page.route('**/*', (route) => {
      const url = new URL(route.request().url());
      if (url.protocol === 'data:' || url.host === own) return route.continue();
      foreign.push(route.request().url());
      return route.abort();
    });
    await use(foreign);
  },
});

export { expect };

export async function cssVar(page: Page, name: string): Promise<string> {
  return page.evaluate((n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim(), name);
}
