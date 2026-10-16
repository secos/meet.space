import { test, expect, cssVar, hosts, isCustomDomain, siteName, platformName, groupName } from './site';

// Phase 0 smoke (P0-E2E-01..03, P0-LAYOUT-12, P0-WHITELABEL-01..03), run on
// the platform and on the custom domain.

test('home page renders, styled, as this site', async ({ page, foreignRequests }, info) => {
  const failed: string[] = [];
  page.on('response', (r) => { if (r.status() >= 400) failed.push(`${r.status()} ${r.url()}`); });
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });

  const res = await page.goto('/');
  expect(res?.status()).toBe(200);
  expect(res?.headers()['cache-control']).toBe('public, s-maxage=300');
  await expect(page).toHaveTitle(siteName(info));
  await expect(page.getByRole('navigation').getByRole('link', { name: new RegExp(siteName(info)) })).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 })).toContainText(siteName(info));

  // The stylesheet applied: the body uses the ground and ink tokens.
  const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  expect(bg).toBe('rgb(245, 247, 250)');
  expect(await cssVar(page, '--color-brand')).toBe('#2563eb');

  // Figtree is served from this host and loads.
  await page.evaluate(() => document.fonts.ready);
  expect(await page.evaluate(() => document.fonts.check('400 16px Figtree'))).toBe(true);
  expect(await page.evaluate(() => document.fonts.check('800 16px Figtree'))).toBe(true);

  expect(foreignRequests, 'requests to other hosts').toEqual([]);
  expect(failed, 'failed requests').toEqual([]);
  expect(errors, 'console errors').toEqual([]);
  // htmx 4 is loaded.
  expect(await page.evaluate(() => typeof (window as any).htmx)).toBe('object');
});

test('custom domain shows no platform identity', async ({ page }, info) => {
  test.skip(!isCustomDomain(info), 'white-label check is for the custom domain');
  for (const path of ['/', '/no-such-page']) {
    await page.goto(path);
    const html = await page.content();
    expect(html.toLowerCase()).not.toContain(platformName.toLowerCase());
    expect(html).not.toContain(hosts.platform + '/');
    expect(html).not.toContain(`//${hosts.platform}`);
    expect(html).not.toContain(hosts.origin);
    await expect(page).toHaveTitle(new RegExp(groupName));
  }
});

test('404 page: one message, one way out, in the site layout', async ({ page }, info) => {
  const res = await page.goto('/no-such-page');
  expect(res?.status()).toBe(404);
  expect(res?.headers()['cache-control']).toBe('no-store, private');
  await expect(page.getByText("We can't find that page.")).toBeVisible();
  const home = page.getByRole('main').getByRole('link', { name: 'Go to the home page' });
  await expect(home).toHaveAttribute('href', '/');
  await expect(page.getByRole('navigation')).toContainText(siteName(info));
});

test('dark mode follows the system setting', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.goto('/');
  const [bg, fg] = await page.evaluate(() => {
    const s = getComputedStyle(document.body);
    return [s.backgroundColor, s.color];
  });
  expect(bg).toBe('rgb(15, 20, 28)');
  expect(fg).toBe('rgb(232, 237, 245)');
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toContain('dark');
});

test('reduced motion: nothing animates', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/');
  expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
});

test('nothing is written to browser storage', async ({ page }) => {
  await page.goto('/');
  await page.goto('/no-such-page');
  const stored = await page.evaluate(() => [localStorage.length, sessionStorage.length]);
  expect(stored).toEqual([0, 0]);
});

test('works with JavaScript off', async ({ browser, baseURL }, info) => {
  const ctx = await browser.newContext({ javaScriptEnabled: false, baseURL });
  const page = await ctx.newPage();
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1 })).toContainText(siteName(info));
  await expect(page.getByRole('contentinfo')).toContainText(siteName(info));
  await ctx.close();
});

// decisions.md "Security headers": the browser refuses to show our pages
// inside another page's frame (clickjacking).
test('pages refuse to be shown in a frame', async ({ page, baseURL }, info) => {
  await page.setContent(`<iframe id="f" src="${baseURL}/" width="600" height="400"></iframe>`);
  const frame = await (await page.waitForSelector('#f')).contentFrame();
  await frame!.waitForLoadState('load');
  expect(frame!.url()).not.toBe(`${baseURL}/`);
  await expect(frame!.getByRole('heading', { level: 1 })).toHaveCount(0);
  await expect(frame!.getByText(siteName(info))).toHaveCount(0);
});
