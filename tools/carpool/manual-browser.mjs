import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { chromium } from 'playwright';
import pg from 'pg';

let input = '';
for await (const chunk of process.stdin) input += chunk;
const { sites, users, dsn, artifacts } = JSON.parse(input);
assert(sites.manual && path.isAbsolute(artifacts));
await mkdir(artifacts, { recursive: true, mode: 0o700 });
const browser = await chromium.launch({ headless: true, channel: process.env.CARPOOL_BROWSER_CHANNEL || 'chrome' });
const db = new pg.Client({ connectionString: dsn });
await db.connect();
const contexts = [];
const results = [];
const errors = [];
const record = name => { results.push(name); console.log(`PASS ${name}`); };
const byID = (page, id) => page.getByTestId(id);
const action = (page, name, id) => page.locator(`[data-test="${name}-${id}"]`);
const past = () => new Date(Date.now() - 120000).toISOString().slice(0, 19);
async function api(site, user, route, body, method = body === undefined ? 'GET' : 'POST', key) {
  const response = await fetch(`${site}/api/v1${route}`, {
    method, headers: { ...(user ? { Authorization: `Bearer ${user.token}` } : {}), 'Content-Type': 'application/json', ...(key ? { 'Idempotency-Key': key } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  const payload = await response.json();
  return { status: response.status, data: payload.data ?? payload };
}
async function context(user, viewport, site = sites.manual, language = 'en') {
  const ctx = await browser.newContext({ viewport, locale: language === 'zh' ? 'zh-CN' : 'en-US', timezoneId: 'UTC', reducedMotion: 'reduce' });
  contexts.push(ctx);
  await ctx.route('**/*', route => ['127.0.0.1', 'localhost'].includes(new URL(route.request().url()).hostname) ? route.continue() : route.abort());
  await ctx.addInitScript(({ user: { token, ...profile }, language }) => {
    localStorage.setItem('auth_token', token); localStorage.setItem('auth_user', JSON.stringify(profile)); localStorage.setItem('sub2api_locale', language);
    localStorage.setItem(`${profile.role === 'admin' ? 'admin' : 'user'}_guide_${profile.id}_${profile.role}_v4_interactive`, 'true');
  }, { user, language });
  const page = await ctx.newPage();
  page.setDefaultTimeout(20000);
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(`${site}${user.role === 'admin' ? '/admin/carpool' : '/carpool'}`);
  return page;
}
async function capture(page, name) {
  await page.evaluate(() => document.fonts.ready);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `${name}: document overflow`);
  const overflowing = await page.locator('[role="dialog"] input, [role="dialog"] textarea, [role="dialog"] button, [data-testid="manual-payment"] dd').evaluateAll(elements => elements.filter(el => {
    const rect = el.getBoundingClientRect(); return rect.width > 0 && (rect.left < -1 || rect.right > innerWidth + 1);
  }).length);
  assert.equal(overflowing, 0, `${name}: control outside viewport`);
  await page.screenshot({ path: path.join(artifacts, `${name}.png`), fullPage: true, animations: 'disabled' });
}
async function close(page) { await page.getByRole('button', { name: 'Close modal', exact: true }).click(); await page.getByRole('dialog').waitFor({ state: 'hidden' }); }
async function create(page, label) {
  await byID(page, 'carpool-create').click();
  assert.equal(await byID(page, 'carpool-price').inputValue(), '1400.00');
  await byID(page, 'carpool-capacity').selectOption('3');
  assert((await byID(page, 'carpool-seat-price-preview').textContent()).includes('466.67'));
  await byID(page, 'carpool-capacity').selectOption('4');
  assert((await byID(page, 'carpool-seat-price-preview').textContent()).includes('350.00'));
  await capture(page, `${label}-total-price-default`);
  for (const [field, value] of Object.entries({ name: `${label} manual car`, price: '199.80', 'service-days': '30', 'delivery-hours': '24', deadline: new Date(Date.now() + 3600000).toISOString().slice(0, 19) })) await byID(page, `carpool-${field}`).fill(value);
  await byID(page, 'carpool-capacity').selectOption('2');
  assert.equal(await byID(page, 'carpool-refund-policy').count(), 0);
  await byID(page, 'manual-refund-policy').waitFor();
  await capture(page, `${label}-create`);
  const response = page.waitForResponse(r => r.url().endsWith('/admin/carpool/cars') && r.request().method() === 'POST');
  await byID(page, 'carpool-save').click();
  const car = (await (await response).json()).data;
  assert.equal(car.enrollment_mode, 'manual'); assert.equal(car.price_minor, 9990); assert.equal(car.total_price_minor, 19980);
  await action(page, 'publish', car.id).click();
  const publish = page.waitForResponse(r => r.url().endsWith(`/cars/${car.id}/publish`));
  await byID(page, 'carpool-publish-confirm').click(); assert.equal((await publish).status(), 200);
  return car;
}
async function prepareEnrollment(page, car, user, label) {
  await action(page, 'enroll', car.id).click();
  await byID(page, 'manual-user-search').fill(user.email);
  await byID(page, 'manual-user-search-submit').click();
  await byID(page, `manual-user-${user.id}`).check();
  await byID(page, 'manual-received-at').fill(past());
  await byID(page, 'manual-reference').fill(`synthetic-${car.id}-${user.id}`);
  await byID(page, 'manual-note').fill(`private-${label}-${user.id}`);
  await byID(page, 'manual-received-confirm').check();
  await capture(page, `${label}-enroll-${user.id}`);
  await byID(page, 'manual-review').click();
  assert((await page.getByRole('dialog').innerText()).includes(user.email));
}
async function confirmEnrollment(page, car) {
  const response = page.waitForResponse(r => r.url().endsWith(`/cars/${car.id}/members`) && r.request().method() === 'POST');
  await byID(page, 'manual-enroll-confirm').click();
  const result = await response; assert.equal(result.status(), 200);
  await page.getByRole('dialog').waitFor({ state: 'hidden' });
  return (await result.json()).data;
}
try {
  for (const [label, viewport, language] of [['desktop', { width: 1440, height: 1000 }, 'en'], ['mobile', { width: 390, height: 844 }, 'zh']]) {
    const admin = await context(users[0], viewport, sites.manual, language);
    const car = await create(admin, label);
    const member = await context(users[1], viewport, sites.manual, language);
    await byID(member, `contact-${car.id}`).click();
    assert((await byID(member, 'carpool-contact-info').innerText()).includes('<b>contact</b>'));
    assert.equal(await byID(member, 'carpool-contact-info').locator('b').count(), 0);
    await capture(member, `${label}-contact`); await close(member);
    assert.equal(await byID(member, `reserve-${car.id}`).count(), 0);
    record(`${label}: manual creation, fixed policy and escaped contact without self-reservation`);

    await prepareEnrollment(admin, car, users[1], label);
    let first;
    if (label === 'desktop') {
      const keys = []; let lost = false;
      await admin.route(`**/cars/${car.id}/members`, async route => {
        if (route.request().method() !== 'POST') return route.continue();
        keys.push(route.request().headers()['idempotency-key']);
        if (!lost) { lost = true; const committed = await route.fetch(); assert.equal(committed.status(), 200); first = (await committed.json()).data; await route.abort('failed'); }
        else await route.continue();
      });
      await byID(admin, 'manual-enroll-confirm').click();
      await admin.getByText('The result is unknown. The original account, amount and request are locked for a safe retry.', { exact: true }).waitFor();
      assert.equal(await byID(admin, 'manual-retry').isEnabled(), true);
      assert(first?.id, 'the original request committed before the response was lost');
      await capture(admin, 'desktop-unknown-receipt');
      await admin.reload(); await byID(admin, 'manual-enroll-resume').click(); await byID(admin, 'manual-retry').click();
      await admin.getByRole('dialog').waitFor({ state: 'hidden' });
      assert.equal(keys.length, 2); assert.equal(keys[0], keys[1]);
      await admin.unroute(`**/cars/${car.id}/members`);
      assert.equal(Number((await db.query('SELECT count(*) FROM carpool_orders WHERE car_id=$1 AND user_id=$2', [car.id, users[1].id])).rows[0].count), 1);
      record('committed lost enrollment survives browser reload and retries without a duplicate');
    } else first = await confirmEnrollment(admin, car);
    await member.reload(); await byID(member, `car-${car.id}`).waitFor();
    const joined = byID(member, `car-${car.id}`).locator('[data-testid="seat-availability"]');
    assert.equal((await joined.textContent()).replace(/\s/g, ''), '1/2');
    await capture(member, `${label}-joined-one`);
    await prepareEnrollment(admin, car, users[2], label);
    const second = await confirmEnrollment(admin, car);
    const full = (await api(sites.manual, users[0], `/admin/carpool/cars/${car.id}`)).data;
    assert.equal(full.status, 'awaiting_deployment'); assert.equal(full.confirmed_count, 2);
    assert.equal(second.membership.service_start, null);
    await capture(admin, `${label}-full`);
    await member.goto(`${sites.manual}/carpool/mine`); await byID(member, `order-${first.id}`).waitFor();
    assert.equal(await byID(member, `cancel-${first.id}`).count(), 0);
    assert(!(await member.locator('main').innerText()).includes(`private-${label}`));
    await capture(member, `${label}-mine-confirmed`);
    record(`${label}: full car waits for deployment; user has receipt but no internal notes or cancellation`);

    await byID(admin, 'manual-orders-tab').click();
    await byID(admin, `withdraw-${first.id}`).click();
    await byID(admin, 'manual-withdraw-reason').fill('Synthetic member withdrawal before service');
    await byID(admin, 'manual-order-ack').check();
    await capture(admin, `${label}-withdraw`);
    const withdrawal = admin.waitForResponse(r => r.url().endsWith(`/orders/${first.id}/withdraw`));
    await byID(admin, 'manual-order-confirm').click(); assert.equal((await withdrawal).status(), 200);
    await admin.getByRole('dialog').waitFor({ state: 'hidden' });
    const review = (await api(sites.manual, users[0], `/admin/carpool/cars/${car.id}`)).data;
    assert.equal(review.status, 'needs_review'); assert.equal(review.full_at, full.full_at); assert.equal(review.delivery_due, full.delivery_due);
    await member.goto(`${sites.manual}/carpool`); await byID(member, `car-${car.id}`).waitFor();
    assert.equal(await byID(member, `contact-${car.id}`).count(), 0);
    await capture(member, `${label}-needs-review`);
    record(`${label}: withdrawal queues a refund and stops recruitment without resetting delivery promises`);

    const refunds = await context(users[0], viewport, sites.readonly, language);
    await byID(refunds, 'manual-orders-tab').click(); await byID(refunds, 'manual-refund-filter').selectOption('pending');
    await byID(refunds, `refund-${first.id}`).click();
    await byID(refunds, 'manual-refund-at').fill(new Date(Date.now() - 1000).toISOString().slice(0, 19));
    await byID(refunds, 'manual-order-ack').check(); await capture(refunds, `${label}-refund`);
    const refundResponse = refunds.waitForResponse(r => r.url().endsWith(`/orders/${first.id}/refund`));
    await byID(refunds, 'manual-order-confirm').click(); assert.equal((await refundResponse).status(), 200);
    await refunds.getByRole('dialog').waitFor({ state: 'hidden' });
    const owned = (await api(sites.readonly, users[1], `/carpool/orders/${first.id}`)).data;
    assert.equal(owned.payment.refund.status, 'refunded'); assert.equal(owned.receipt, undefined);
    const historical = await context(users[1], viewport, sites.readonly, language);
    await historical.goto(`${sites.readonly}/carpool/mine`); await byID(historical, `order-${first.id}`).waitFor();
    await capture(historical, `${label}-mine-refunded`);
    record(`${label}: disabled additions still allow refund registration and user history`);

    await admin.goto(`${sites.manual}/admin/carpool`); await action(admin, 'cancel', car.id).click();
    await byID(admin, 'carpool-cancel-reason').fill('Synthetic cancelled vehicle'); await byID(admin, 'carpool-confirm-members').check();
    const cancelled = admin.waitForResponse(r => r.url().endsWith(`/cars/${car.id}/cancel`));
    await byID(admin, 'carpool-cancel-confirm').click(); assert.equal((await cancelled).status(), 200);
    await byID(admin, 'manual-orders-tab').click(); await byID(admin, 'manual-refund-filter').selectOption('pending');
    await byID(admin, `refund-${second.id}`).waitFor(); await capture(admin, `${label}-cancelled-refund-queue`);
    record(`${label}: cancelled vehicles remain in the pending refund work list`);
  }

  const terms = { name: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.repeat(4), capacity: 4, price_minor: 1000000000000, currency: 'CNY', service_days: 3650, recruit_until: new Date(Date.now() + 3600000).toISOString(), delivery_hours: 8760 };
  const large = await api(sites.manual, users[0], '/admin/carpool/cars', terms); assert.equal(large.status, 200);
  await api(sites.manual, users[0], `/admin/carpool/cars/${large.data.id}/publish`, {});
  const narrow = await context(users[1], { width: 320, height: 780 }, sites.manual, 'en');
  await byID(narrow, `car-${large.data.id}`).waitFor(); await capture(narrow, 'narrow-long-terms');
  const legacy = await api(sites.main, users[0], '/admin/carpool/cars', { ...terms, name: 'Legacy test hidden from manual', refund_policy: 'Synthetic test only' });
  await api(sites.main, users[0], `/admin/carpool/cars/${legacy.data.id}/publish`, {});
  const catalog = await api(sites.manual, users[1], '/carpool/cars');
  assert(catalog.data.items.every(car => car.enrollment_mode === 'manual'));
  assert.equal((await api(sites.manual, users[1], `/carpool/cars/${large.data.id}/reservations`, {}, 'POST', 'blocked')).status, 404);
  assert.equal((await api(sites.manual, users[1], '/admin/carpool/orders')).status, 403);
  assert.equal((await api(sites.manual, null, '/admin/carpool/orders')).status, 401);
  assert.equal(Number((await db.query('SELECT count(*) FROM carpool_node_memberships')).rows[0].count), 0);
  assert((await db.query('SELECT balance FROM users')).rows.every(row => Number(row.balance) === 10));
  record('320px long terms, manual/test isolation, admin permissions and no node/balance side effects');
  assert.deepEqual(errors, []);
  await writeFile(path.join(artifacts, 'results.json'), JSON.stringify({ checks: results, pageErrors: errors }, null, 2), { mode: 0o600 });
} finally {
  for (const context of contexts) await context.close();
  await browser.close(); await db.end();
}
