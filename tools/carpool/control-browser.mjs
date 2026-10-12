import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import path from 'node:path';
import { chromium } from 'playwright';

let raw = '';
for await (const chunk of process.stdin) raw += chunk;
const { site, users, cars, artifacts, nativeFlow, backgroundSync } = JSON.parse(raw);
assert.equal(new URL(site).hostname, '127.0.0.1');
assert(path.isAbsolute(artifacts));
const browser = await chromium.launch({ headless: true, channel: process.env.CARPOOL_BROWSER_CHANNEL || 'chrome' });
const contexts = [];
const errors = [];
const results = [];
const record = name => { results.push(name); console.log(`PASS ${name}`); };
const field = (page, id) => page.getByTestId(id);
async function api(page, route, body, method = body === undefined ? 'GET' : 'POST', idempotencyKey) {
  return page.evaluate(async ({ route, body, method, idempotencyKey }) => {
    const headers = { Authorization: `Bearer ${localStorage.getItem('auth_token')}`, 'Content-Type': 'application/json' };
    if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey;
    const response = await fetch(`/api/v1${route}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const value = response.headers.get('content-type')?.includes('application/json') ? await response.json() : await response.text();
    return { status: response.status, data: value.data ?? value };
  }, { route, body, method, idempotencyKey });
}
async function manualRoutes(admin, member) {
  const terms = { name: 'Manual route acceptance', capacity: 4, total_price_minor: 140000, currency: 'CNY', service_days: 30, recruit_until: new Date(Date.now() + 3600000).toISOString(), delivery_hours: 24 };
  const created = await api(admin, '/admin/carpool/cars', terms);
  assert.equal(created.status, 200, 'preview must expose real car creation');
  const id = created.data.id;
  assert.equal(created.data.price_minor, 35000);
  assert.equal((await api(admin, `/admin/carpool/cars/${id}`, { ...terms, name: 'Manual route updated' }, 'PATCH')).status, 200);
  assert.equal((await api(admin, `/admin/carpool/cars/${id}/publish`, {})).status, 200);
  const found = await api(admin, `/admin/users?status=active&search=${encodeURIComponent(users[1].email)}`);
  assert.equal(found.status, 200); assert(found.data.items.some(user => user.id === users[1].id));
  assert.equal((await api(admin, `/admin/users/${users[1].id}`)).status, 200);
  const joined = await api(admin, `/admin/carpool/cars/${id}/members`, { user_id: users[1].id, amount_minor: 35000, channel: 'wechat', received_at: new Date(Date.now() - 1000).toISOString(), confirm_received: true }, 'POST', `join-${id}`);
  assert.equal(joined.status, 200);
  assert.equal((await api(admin, `/admin/carpool/cars/${id}/members`)).status, 200);
  assert.equal((await api(admin, `/admin/carpool/orders?car_id=${id}`)).status, 200);
  assert.equal((await api(admin, `/admin/carpool/orders/${joined.data.id}`)).status, 200);
  assert.equal((await api(member, `/carpool/cars/${id}`)).status, 200);
  assert.equal((await api(member, `/carpool/orders/${joined.data.id}`)).status, 200);
  assert.equal((await api(member, `/carpool/cars/${id}/reservations`, {})).status, 404);
  assert.equal((await api(admin, `/admin/carpool/orders/${joined.data.id}/withdraw`, { reason: 'Synthetic route verification', confirm: true }, 'POST', `withdraw-${id}`)).status, 200);
  assert.equal((await api(admin, `/admin/carpool/orders/${joined.data.id}/refund`, { amount_minor: 35000, refunded_at: new Date(Date.now() - 1000).toISOString(), confirm_refunded: true }, 'POST', `refund-${id}`)).status, 200);
  assert.equal((await api(admin, `/admin/carpool/cars/${id}/cancel`, { reason: 'Synthetic route verification', confirm_members: true })).status, 200);
  record('preview manual car CRUD, member lookup/enrollment, receipt/withdrawal/refund and disabled self-reservation');
}
async function settle(page, car) {
  if (!backgroundSync) {
    assert.equal((await api(page, '/admin/__test__/drain', {})).status, 200);
    return;
  }
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    const result = await api(page, `/admin/carpool/cars/${car.id}/sync`);
    assert.equal(result.status, 200);
    if (result.data.items.length === car.capacity && result.data.items.every(member => member.delivery === 'synced' && member.ack_version === member.version)) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error('Production synchronization worker did not settle without the fixture drain endpoint');
}
async function login(user, viewport, language) {
  const context = await browser.newContext({ viewport, locale: language === 'zh' ? 'zh-CN' : 'en-US', timezoneId: 'UTC', reducedMotion: 'reduce' });
  contexts.push(context);
  await context.route('**/*', route => new URL(route.request().url()).origin === site ? route.continue() : route.abort());
  await context.addInitScript(({ language, user }) => {
    localStorage.setItem('sub2api_locale', language);
    localStorage.setItem(`${user.role === 'admin' ? 'admin' : 'user'}_guide_${user.id}_${user.role}_v4_interactive`, 'true');
  }, { language, user });
  const page = await context.newPage();
  page.setDefaultTimeout(20000);
  page.on('pageerror', error => errors.push(error.message));
  const destination = user.role === 'admin' ? '/admin/carpool' : '/carpool/mine';
  await page.goto(`${site}/login?redirect=${encodeURIComponent(destination)}`);
  await page.locator('#email').fill(user.email);
  await page.locator('#password').fill(user.password);
  const response = page.waitForResponse(r => r.url().endsWith('/auth/login') && r.request().method() === 'POST');
  await page.locator('button[type="submit"]').click();
  assert.equal((await response).status(), 200);
  await page.waitForURL(`${site}${destination}`);
  const notification = page.getByRole('button', { name: 'Close notification', exact: true });
  if (await notification.count()) { await notification.first().click(); await notification.waitFor({ state: 'hidden' }); }
  return page;
}
async function capture(page, name) {
  await page.evaluate(() => document.fonts.ready);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `${name}: page overflow`);
  const overflow = await page.locator('[role="dialog"] input, [role="dialog"] button, [role="dialog"] select, [role="dialog"] h3').evaluateAll(nodes => nodes.filter(node => {
    const rect = node.getBoundingClientRect();
    return rect.width > 0 && (rect.left < -1 || rect.right > innerWidth + 1 || node.scrollWidth > node.clientWidth + 2 && node.tagName !== 'INPUT');
  }).map(node => node.tagName));
  assert.deepEqual(overflow, [], `${name}: clipped control or title`);
  assert.equal(await field(page, 'console-secret').count(), 0, 'never capture a full key');
  await page.screenshot({ path: path.join(artifacts, `${name}.png`), fullPage: false, animations: 'disabled' });
}
async function close(page) { await page.getByRole('button', { name: 'Close modal', exact: true }).click(); await page.getByRole('dialog').waitFor({ state: 'hidden' }); }
async function openConsole(page, car) {
  const mine = await api(page, '/carpool/mine');
  assert.equal(mine.status, 200);
  const order = mine.data.items.find(item => item.car_id === car.id);
  assert(order?.membership);
  await field(page, `console-${order.id}`).click();
  await field(page, 'console-access').waitFor();
  return order;
}
try {
  let lastAdmin;
  let lastMember;
  for (const [label, viewport, language] of [['desktop', { width: 1440, height: 1000 }, 'en'], ['mobile', { width: 390, height: 844 }, 'zh'], ['narrow', { width: 320, height: 800 }, 'en']]) {
    const admin = await login(users[0], viewport, language);
    const member = await login(users[1], viewport, language);
    lastAdmin = admin; lastMember = member;
    record(`${label}: email/password login at A only`);
    if (label === 'desktop') await manualRoutes(admin, member);
    await openConsole(member, cars[2]);
    assert.equal(await field(member, 'console-create').isDisabled(), true);
    await capture(member, `${label}-prepared`); await close(member);

    await admin.locator(`[data-test="node-${cars[0].id}"]`).click();
    await field(admin, 'node-secret').waitFor();
    assert.equal(await field(admin, 'node-secret').inputValue(), '');
    assert.equal(await field(admin, 'node-secret').getAttribute('type'), 'password');
    await capture(admin, `${label}-connection`);
    const saved = admin.waitForResponse(r => r.url().includes('/connection') && r.request().method() === 'PUT');
    await field(admin, 'node-save').click(); assert.equal((await saved).status(), 200);
    const checked = admin.waitForResponse(r => r.url().endsWith('/connection/check'));
    await field(admin, 'node-check').click(); assert.equal((await checked).status(), 200);
    assert.equal(await field(admin, 'node-secret').inputValue(), ''); await close(admin);
    if (nativeFlow) {
      for (const car of cars.slice(0, 2)) {
        await admin.locator(`[data-test="node-${car.id}"]`).click();
        await field(admin, 'node-account').waitFor();
        const status = await api(admin, `/admin/carpool/cars/${car.id}/native`);
        assert.equal(status.status, 200);
        if (!status.data.service_start) {
          const options = await api(admin, `/admin/carpool/cars/${car.id}/native/accounts`);
          assert.equal(options.status, 200); assert.equal(options.data.length, 1);
          await field(admin, 'node-account').selectOption(String(options.data[0].id));
          await capture(admin, `${label}-native-before-prepare`);
          const prepared = admin.waitForResponse(r => r.url().endsWith(`/cars/${car.id}/native/prepare`) && r.request().method() === 'POST');
          await field(admin, 'node-prepare').click(); assert.equal((await prepared).status(), 200);
          await settle(admin, car);
          await field(admin, 'node-refresh').click();
          try {
            await field(admin, 'node-activate-confirm').check();
          } catch (error) {
            console.error('Native readiness:', JSON.stringify(await api(admin, `/admin/carpool/cars/${car.id}/native`)));
            console.error('Member deliveries:', JSON.stringify(await api(admin, `/admin/carpool/cars/${car.id}/sync`)));
            await capture(admin, `${label}-activation-blocked`);
            throw error;
          }
          await capture(admin, `${label}-native-ready`);
          const activated = admin.waitForResponse(r => r.url().endsWith(`/cars/${car.id}/native/activate`) && r.request().method() === 'POST');
          await field(admin, 'node-activate').click(); assert.equal((await activated).status(), 200);
        }
        await settle(admin, car);
        const refreshed = admin.waitForResponse(r => r.url().endsWith(`/cars/${car.id}/native/quota/sync`) && r.request().method() === 'POST');
        await field(admin, 'node-quota-sync').click(); assert.equal((await refreshed).status(), 200);
        assert.equal(await field(admin, 'node-weekly-limit').textContent(), (1800 / car.capacity).toFixed(2));
        await capture(admin, `${label}-native-active`); await close(admin);
      }
      record(`${label}: native group preparation and explicit fixed-term subscription activation`);
    } else {
      assert.equal((await api(admin, '/admin/__test__/activate', {})).status, 200);
    }
    record(`${label}: masked connection, blank credential preservation and explicit check`);

    await openConsole(member, cars[0]);
    if (nativeFlow) {
      await field(member, 'console-quota-tab').click();
      await field(member, 'console-weekly-limit').waitFor();
      await member.locator('[role="dialog"] p[role="status"].text-gray-500').waitFor({ state: 'hidden' });
      assert.equal((await field(member, 'console-weekly-used').textContent()).trim(), '0.00');
      assert.equal((await field(member, 'console-weekly-limit').textContent()).trim(), (1800 / cars[0].capacity).toFixed(2));
      assert.equal((await field(member, 'console-account-utilization').textContent()).trim(), '0.0%');
      assert.equal((await field(member, 'console-account-estimate').textContent()).trim(), '-');
      await capture(member, `${label}-native-quota`);
      await field(member, 'console-reset-countdown').scrollIntoViewIfNeeded();
      await capture(member, `${label}-native-quota-window`);
      await field(member, 'console-keys-tab').click();
      record(`${label}: own native quota, fresh account zero and manual cycle synchronization`);
    }
    await field(member, 'console-name').fill(`${label}-` + 'workstation-'.repeat(7));
    let created;
    const operations = [];
    if (label === 'desktop') {
      let lost = false;
      await member.route(`**/carpool/cars/${cars[0].id}/keys`, async route => {
        if (route.request().method() !== 'POST') return route.continue();
        operations.push(route.request().headers()['idempotency-key']);
        if (!lost) { lost = true; const response = await route.fetch(); assert.equal(response.status(), 200); created = (await response.json()).data; await route.abort('failed'); }
        else await route.continue();
      });
      await field(member, 'console-create').click();
      await member.getByText('Request failed. The result may be unknown. Retry to check the same request.', { exact: true }).waitFor();
      await member.reload(); await openConsole(member, cars[0]);
      await field(member, 'console-create').click();
      await field(member, 'console-secret').waitFor();
      assert.equal(operations.length, 2); assert.equal(operations[0], operations[1]);
      await member.unroute(`**/carpool/cars/${cars[0].id}/keys`);
      record('lost committed key response recovers after reload without a duplicate');
    } else {
      const response = member.waitForResponse(r => r.url().endsWith(`/cars/${cars[0].id}/keys`) && r.request().method() === 'POST');
      await field(member, 'console-create').click();
      assert.equal((await response).status(), 200); created = (await (await response).json()).data;
      await field(member, 'console-secret').waitFor();
    }
    const fullKey = await field(member, 'console-secret').inputValue();
    assert(fullKey.length > 10);
    assert.equal(await member.evaluate(value => JSON.stringify(localStorage).includes(value) || JSON.stringify(sessionStorage).includes(value), fullKey), false);
    await field(member, 'console-usage-tab').click(); await field(member, 'console-usage-stats').waitFor();
    assert.equal(await field(member, 'console-secret').count(), 0);
    await capture(member, `${label}-zero-usage`);
    await field(member, 'console-keys-tab').click();
    await field(member, `console-rename-${created.id}`).click();
    await field(member, 'console-rename-input').fill(`${label}-renamed`);
    const renamed = member.waitForResponse(r => r.url().endsWith(`/keys/${created.id}`) && r.request().method() === 'PATCH');
    await field(member, 'console-rename-save').click(); assert.equal((await renamed).status(), 200);
    await field(member, `console-toggle-${created.id}`).click();
    await member.getByText(language === 'zh' ? `#${created.id} · 已停用` : `#${created.id} · Disabled`, { exact: false }).waitFor();
    await capture(member, `${label}-keys`);
    await field(member, `console-delete-${created.id}`).click(); await capture(member, `${label}-delete`);
    const deleted = member.waitForResponse(r => r.url().endsWith(`/keys/${created.id}`) && r.request().method() === 'DELETE');
    await field(member, 'console-delete-confirm').click(); assert.equal((await deleted).status(), 200);
    await field(member, `console-delete-${created.id}`).waitFor({ state: 'hidden' });
    await close(member);
    record(`${label}: create, transient key, usage, rename, disable and confirmed deletion`);

    await openConsole(member, cars[1]);
    const listC = await api(member, `/carpool/cars/${cars[1].id}/keys`);
    assert.equal(listC.status, 200); assert.equal(listC.data.total, 0);
    await close(member);
    record(`${label}: B and C key lists remain separate`);
  }
  const other = await login(users[2], { width: 1280, height: 900 }, 'en');
  const forbidden = await api(other, '/admin/carpool/control');
  assert.equal(forbidden.status, 403);
  await openConsole(other, cars[0]);
  assert.equal((await api(other, `/carpool/cars/${cars[0].id}/keys`)).data.total, 0);
  await close(other);
  assert.equal((await api(lastAdmin, '/admin/__test__/stop-c', {})).status, 200);
  await openConsole(lastMember, cars[1]);
  await field(lastMember, 'console-usage-tab').click();
  await lastMember.getByText('Usage is unavailable. No totals are shown for this request.', { exact: true }).waitFor();
  assert.equal(await field(lastMember, 'console-usage-stats').count(), 0);
  await capture(lastMember, 'narrow-node-unavailable');
  record('member isolation, admin denial and unavailable usage distinct from zero');
  assert.deepEqual(errors, [], 'no browser page errors');
  await writeFile(path.join(artifacts, 'results.json'), JSON.stringify({ results, pageErrors: errors }, null, 2), { mode: 0o600 });
} finally {
  for (const context of contexts) await context.close();
  await browser.close();
}
