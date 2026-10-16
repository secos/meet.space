import { test, expect, hosts, port } from './site';
import type { APIRequestContext } from '@playwright/test';

// Requests go to the loopback server with an explicit Host header, so no
// name is ever resolved through DNS.
function get(request: APIRequestContext, host: string, path: string) {
  return request.get(`http://127.0.0.1:${port}${path}`, { headers: { Host: host } });
}

// Host isolation (CLAUDE.md "What must be covered" 3; decisions.md "/health"
// and "Error pages"), checked over real HTTP. Host-level, so it runs once.

test.describe('hosts', () => {
  test.beforeEach(({}, info) => {
    test.skip(info.project.name !== 'platform', 'host checks run once');
  });

  test('origin host serves only /health', async ({ request }) => {
    const health = await get(request, hosts.origin, '/health');
    expect(health.status()).toBe(200);
    expect(await health.text()).toBe('ok\n');
    expect(health.headers()['cache-control']).toBe('no-store, private');
    for (const path of ['/', '/static/js/htmx.min.js', '/no-such-page']) {
      const res = await get(request, hosts.origin, path);
      expect(res.status(), path).toBe(404);
      expect(await res.text()).not.toContain('meet.space');
    }
  });

  test('/health is not served on public hosts', async ({ request }) => {
    for (const h of [hosts.platform, hosts.group]) {
      expect((await get(request, h, '/health')).status(), h).toBe(404);
    }
  });

  // decisions.md "Security headers": on every host kind and status.
  test('every response carries the security headers', async ({ request }) => {
    const cases: [string, string][] = [
      [hosts.platform, '/'], [hosts.platform, '/static/js/htmx.min.js'], [hosts.platform, '/no-such-page'],
      [hosts.group, '/'], [hosts.group, '/no-such-page'],
      [hosts.origin, '/health'], [hosts.origin, '/'],
      [`unknown.localhost:${port}`, '/'],
    ];
    for (const [host, path] of cases) {
      const h = (await get(request, host, path)).headers();
      expect(h['x-content-type-options'], host + path).toBe('nosniff');
      expect(h['content-security-policy'], host + path).toBe("frame-ancestors 'none'");
      expect(h['x-frame-options'], host + path).toBe('DENY');
    }
  });

  test('unknown host gets the bare 404', async ({ request }) => {
    const res = await get(request, `unknown.localhost:${port}`, '/');
    expect(res.status()).toBe(404);
    expect(res.headers()['cache-control']).toBe('no-store, private');
    const body = await res.text();
    expect(body).toContain('Page not found.');
    expect(body).not.toContain('meet.space');
    expect(body).not.toContain('<a');
  });
});
