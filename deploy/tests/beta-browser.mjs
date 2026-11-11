// Production-page checks against an explicitly supplied anonymous test instance.
// Does not start any local fixture, Vite server, or application process.
import assert from 'node:assert/strict';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
process.env.PLAYWRIGHT_BROWSERS_PATH ??= path.join(root, '.runtime/cache/playwright');
const { chromium } = await import(path.join(root, 'web/node_modules/playwright/index.mjs'));
const [origin, credentialsPath, output, mode] = process.argv.slice(2);
const loopback = /^http:\/\/127\.0\.0\.1:\d+$/.test(origin);
assert.ok(mode === '--anonymous-loopback' ? loopback : !mode && /^http:\/\/192\.168\.[0-9.]+:19400$/.test(origin), 'explicit anonymous NAS origin or opted-in loopback fixture required');
assert.ok(!['8080', '5173', '8081', '5174'].includes(new URL(origin).port), 'daily and historical pilot ports are forbidden');
const credentials = JSON.parse(await readFile(credentialsPath, 'utf8'));
await mkdir(output, { recursive: false, mode: 0o700 });
const browser = await chromium.launch();
const records = [];
try {
  for (const [name, viewport, dark] of [['desktop-light', { width: 1280, height: 900 }, false], ['desktop-dark', { width: 1440, height: 900 }, true], ['mobile-light', { width: 390, height: 844 }, false], ['mobile-dark', { width: 390, height: 844 }, true]]) {
    const context = await browser.newContext({ viewport, isMobile: name.startsWith('mobile'), hasTouch: name.startsWith('mobile'), reducedMotion: 'reduce', colorScheme: dark ? 'dark' : 'light' });
    const page = await context.newPage();
    const errors = [];
    const csp = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => { if (/Content Security Policy|Refused to (execute|apply|load)/i.test(message.text())) csp.push(message.text()); });
    const response = await page.goto(origin + '/admin/books');
    assert.equal(response.status(), 200);
    assert.match(response.headers()['content-security-policy'] || '', /default-src/);
    await page.getByLabel('用户名', { exact: true }).fill(credentials.username);
    await page.getByLabel('密码', { exact: true }).fill(credentials.password);
    const login = page.waitForResponse(response => response.url() === origin + '/api/v1/auth/login' && response.request().method() === 'POST');
    await page.getByRole('button', { name: '登录', exact: true }).click();
    assert.equal((await login).status(), 200, 'production browser login');
    await page.getByRole('switch', { name: '深色主题' }).waitFor();
    const currentDark = (await page.getByRole('switch', { name: '深色主题' }).getAttribute('aria-checked')) === 'true';
    assert.equal(currentDark, dark, 'theme follows explicit test context');
    await page.getByRole('switch', { name: '深色主题' }).click();
    await page.locator('.admin-shell.' + (dark ? 'light' : 'dark')).waitFor();
    await page.getByRole('switch', { name: '深色主题' }).click();
    await page.locator('.admin-shell.' + (dark ? 'dark' : 'light')).waitFor();
    for (const route of ['books', 'libraries', 'versions', 'series', 'tags', 'settings', 'maintenance']) {
      await page.goto(origin + '/admin/' + route);
      await page.locator('.admin-content').waitFor();
      await page.locator('.admin-shell.' + (dark ? 'dark' : 'light')).waitFor();
      assert.equal(await page.getByRole('switch', { name: '深色主题' }).getAttribute('aria-checked'), String(dark));
      await page.waitForTimeout(700);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
      assert.equal(overflow, false, name + ' horizontal overflow ' + route);
      await page.screenshot({ path: path.join(output, name + '-' + route + '.png'), fullPage: true });
      records.push({ viewport: name, route, dark, horizontalOverflow: overflow });
      if (route === 'books' && (name === 'desktop-light' || name === 'mobile-light')) {
        const cover = page.locator('.book-cover-button').filter({ has: page.locator('img') }).first();
        await cover.click();
        const detail = page.getByRole('dialog').filter({ hasText: '漫画详情' });
        await detail.waitFor();
        await detail.getByText('本地文件', { exact: true }).waitFor();
        await page.screenshot({ path: path.join(output, name + '-book-detail.png'), fullPage: true });
        await detail.getByRole('button', { name: '关闭', exact: true }).click();
        await detail.waitFor({ state: 'hidden' });
        records.push({ viewport: name, interaction: 'open-complete-book-detail-and-close', passed: true });
      }
    }
    await page.keyboard.press('Tab');
    assert.notEqual(await page.evaluate(() => document.activeElement?.tagName), 'BODY');
    assert.deepEqual(errors, [], name + ' unhandled browser errors');
    assert.deepEqual(csp, [], name + ' CSP violations');
    await context.close();
  }
} finally { await browser.close(); }
await writeFile(path.join(output, 'results.json'), JSON.stringify({ records, note: 'Screenshots require separate visual review; these assertions are not a complete accessibility audit.' }, null, 2), { mode: 0o600 });
console.log('PASS production login, seven deep links, four viewport/theme combinations, keyboard focus and CSP');
