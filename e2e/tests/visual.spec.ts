import { test, expect } from './site';

// Visual matrix (CLAUDE.md "The browser"; design.md §10 Phase 0): every
// Phase 0 page at 320, 360, 768 and 1024 px, light and dark, with reduced
// motion on. Each cell asserts no horizontal scroll and the shell's
// landmarks, and saves a screenshot to e2e/screenshots for a person to look
// at.
const widths = [320, 360, 768, 1024];
const schemes = ['light', 'dark'] as const;
const pages = [
  { name: 'home', path: '/' },
  { name: '404', path: '/no-such-page' },
];

for (const p of pages) {
  for (const width of widths) {
    for (const scheme of schemes) {
      test(`${p.name} at ${width}px ${scheme}`, async ({ page }, info) => {
        await page.setViewportSize({ width, height: 800 });
        await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' });
        await page.goto(p.path);
        await page.evaluate(() => document.fonts.ready);
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        expect(overflow, 'horizontal scroll').toBeLessThanOrEqual(0);
        for (const role of ['navigation', 'main', 'contentinfo'] as const) {
          await expect(page.getByRole(role)).toBeVisible();
        }
        const nav = await page.getByRole('navigation').boundingBox();
        expect(nav!.height).toBeGreaterThanOrEqual(56);
        await page.screenshot({
          path: `screenshots/${info.project.name}-${p.name}-${width}-${scheme}.png`,
          fullPage: true,
        });
      });
    }
  }
}
