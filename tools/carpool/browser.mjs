import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { chromium } from 'playwright';
import pg from 'pg';

// Receives private fixture credentials on stdin, never via command-line arguments.
let input = '';
for await (const chunk of process.stdin) input += chunk;
const config = JSON.parse(input);
const { sites, users, dsn, cli, artifacts } = config;
assert(sites.main && users.length >= 3 && path.isAbsolute(artifacts));
await mkdir(artifacts, { recursive: true, mode: 0o700 });
const browser = await chromium.launch({ headless: true, channel: process.env.CARPOOL_BROWSER_CHANNEL || 'chrome' });
const db = new pg.Client({ connectionString: dsn });
await db.connect();
const results = [];
const layoutMeasurements = [];
const contexts = [];
const errors = [];
const record = name => { results.push(name); console.log(`PASS ${name}`); };
const byID = (page, id) => page.getByTestId(id);
const rowAction = (page, action, id) => page.locator(`[data-test="${action}-${id}"]`);

async function api(site, user, route, body, method = body === undefined ? 'GET' : 'POST', headers = {}) {
  const response = await fetch(`${site}/api/v1${route}`, {
    method, headers: { Authorization: `Bearer ${user.token}`, 'Content-Type': 'application/json', ...headers },
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  const payload = await response.json();
  return { status: response.status, data: payload.data ?? payload };
}
async function context(user, viewport, site = sites.main, language = 'en') {
  const ctx = await browser.newContext({ viewport, locale: language === 'zh' ? 'zh-CN' : 'en-US', timezoneId: 'UTC', reducedMotion: 'reduce' });
  contexts.push(ctx);
  await ctx.route('**/*', route => {
    const hostname = new URL(route.request().url()).hostname;
    return ['127.0.0.1', 'localhost'].includes(hostname) ? route.continue() : route.abort();
  });
  if (user) await ctx.addInitScript(({ user: { token, ...profile }, language }) => {
    localStorage.setItem('auth_token', token);
    localStorage.setItem('auth_user', JSON.stringify(profile));
    localStorage.setItem('sub2api_locale', language);
    localStorage.setItem(`${profile.role === 'admin' ? 'admin' : 'user'}_guide_${profile.id}_${profile.role}_v4_interactive`, 'true');
  }, { user, language });
  const page = await ctx.newPage();
  page.setDefaultTimeout(20000);
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(`${site}${user?.role === 'admin' ? '/admin/carpool' : '/carpool'}`);
  return page;
}
async function capture(page, name) {
  await page.evaluate(() => document.fonts.ready);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1);
  assert.equal(overflow, false, `${name}: horizontal document overflow`);
  await page.screenshot({ path: path.join(artifacts, `${name}.png`), fullPage: true, animations: 'disabled' });
}
async function confirmCLI(orderID) {
  await new Promise((resolve, reject) => {
    const child = spawn(cli, [], {
      env: { ...process.env, CARPOOL_MAIN_ENABLED: 'true', CARPOOL_MAIN_TEST_ORDERS_ENABLED: 'true', CARPOOL_NODE_ENABLED: 'false', CARPOOL_DATABASE_DSN: dsn },
      stdio: ['pipe', 'pipe', 'pipe']
    });
    let stdout = '';
    child.stdout.on('data', data => { stdout += data; });
    child.on('error', reject);
    child.on('exit', code => {
      if (code !== 0) { reject(new Error('Test confirmation CLI failed')); return; }
      try { assert.equal(JSON.parse(stdout).status, 'confirmed'); resolve(); } catch (error) { reject(error); }
    });
    child.stdin.end(JSON.stringify({ order_id: orderID }));
  });
}
async function refresh(page) { await page.getByRole('button', { name: 'Refresh', exact: true }).click(); }
async function reserve(page, carID) {
  await byID(page, `reserve-${carID}`).click();
  const response = page.waitForResponse(r => r.url().endsWith(`/cars/${carID}/reservations`) && r.request().method() === 'POST');
  await byID(page, 'confirm-reserve').click();
  const payload = await (await response).json();
  await page.waitForURL('**/carpool/mine');
  return payload.data;
}

try {
  for (const [label, viewport] of [['desktop', { width: 1440, height: 1000 }], ['mobile', { width: 390, height: 844 }]]) {
    const admin = await context(users[0], viewport);
    await byID(admin, 'carpool-create').click();
    await byID(admin, 'carpool-name').fill(`${label} local test car`);
    await byID(admin, 'carpool-capacity').selectOption('2');
    await byID(admin, 'carpool-price').fill('199.80');
    await byID(admin, 'carpool-service-days').fill('30');
    await byID(admin, 'carpool-delivery-hours').fill('24');
    await byID(admin, 'carpool-deadline').fill(new Date(Date.now() + 3600000).toISOString().slice(0, 19));
    await byID(admin, 'carpool-refund-policy').fill('Synthetic test only. Cancel before confirmation; no real payment or refund.');
    await capture(admin, `${label}-admin-create`);
    let car;
    if (label === 'desktop') {
      await admin.route('**/api/v1/admin/carpool/cars', async route => {
        if (route.request().method() !== 'POST') { await route.continue(); return; }
        const committed = await route.fetch();
        car = (await committed.json()).data;
        await route.abort('failed');
      });
      await byID(admin, 'carpool-save').click();
      await admin.getByRole('alert').filter({ hasText: 'Creation result is unknown' }).waitFor();
      assert(await byID(admin, 'carpool-save').isDisabled());
      await byID(admin, 'carpool-review-create').click();
      await admin.getByRole('dialog').waitFor({ state: 'hidden' });
      await admin.unroute('**/api/v1/admin/carpool/cars');
      assert.equal(Number((await db.query('SELECT count(*) FROM carpool_cars WHERE name=$1', [`${label} local test car`])).rows[0].count), 1);
      record('uncertain committed administrator creation is reconciled without duplicate draft');
    } else {
      const createResponse = admin.waitForResponse(r => r.url().endsWith('/admin/carpool/cars') && r.request().method() === 'POST');
      await byID(admin, 'carpool-save').click();
      car = (await (await createResponse).json()).data;
    }
    assert(car?.id, 'UI create must return a persisted car');
    assert.equal(car.price_minor, 9990);
    assert.equal(car.total_price_minor, 19980);
    await rowAction(admin, 'edit', car.id).click();
    await byID(admin, 'carpool-name').fill(`${label} revised test car`);
    const editResponse = admin.waitForResponse(r => r.url().endsWith(`/cars/${car.id}`) && r.request().method() === 'PATCH');
    await byID(admin, 'carpool-save').click();
    assert.equal((await (await editResponse).json()).data.name, `${label} revised test car`);
    await rowAction(admin, 'publish', car.id).click();
    const publishResponse = admin.waitForResponse(r => r.url().endsWith(`/cars/${car.id}/publish`));
    await byID(admin, 'carpool-publish-confirm').click();
    assert.equal((await (await publishResponse).json()).data.status, 'recruiting');
    await byID(admin, 'carpool-status-filter').selectOption('');
    record(`${label}: create, edit, publish via administrator UI`);

    const first = await context(users[1], viewport);
    await byID(first, `reserve-${car.id}`).waitFor();
    await capture(first, `${label}-lobby`);
    assert(!(await first.locator('main').innerText()).includes(users[2].email), 'Lobby leaks another identity');
    let held;
    if (label === 'desktop') {
      const keys = [];
      let loseResponse = true;
      await first.route(`**/cars/${car.id}/reservations`, async route => {
        keys.push(route.request().headers()['idempotency-key']);
        if (loseResponse) {
          loseResponse = false;
          const committed = await route.fetch();
          held = (await committed.json()).data;
          assert.equal(committed.status(), 200);
          await route.abort('failed');
        } else await route.continue();
      });
      await byID(first, `reserve-${car.id}`).click();
      await byID(first, 'confirm-reserve').click();
      await first.getByRole('alert').filter({ hasText: 'Request failed' }).waitFor();
      await capture(first, 'desktop-reservation-network-retry');
      // Once the refreshed catalog discovers the committed reservation, the dialog
      // cannot reserve again. The durable result is visible under My cars.
      await first.goto(`${sites.main}/carpool/mine`);
      await byID(first, `order-${held.id}`).waitFor();
      const replay = await api(sites.main, users[1], `/carpool/cars/${car.id}/reservations`, {}, 'POST', { 'Idempotency-Key': keys[0] });
      assert.equal(replay.data.id, held.id);
      record('committed reservation survives lost response; same-key replay keeps one order');
    } else held = await reserve(first, car.id);
    await byID(first, `cancel-${held.id}`).click();
    const cancelled = first.waitForResponse(r => r.url().endsWith(`/orders/${held.id}/cancel`));
    await byID(first, 'confirm-cancel-order').click();
    assert.equal((await (await cancelled).json()).data.status, 'cancelled');
    await first.goto(`${sites.main}/carpool`);
    const expiring = await reserve(first, car.id);
    await db.query("UPDATE carpool_orders SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", [expiring.id]);
    await db.query("UPDATE carpool_seats SET held_until=clock_timestamp()-interval '1 second' WHERE order_id=$1", [expiring.id]);
    await refresh(first);
    await byID(first, `order-${expiring.id}`).getByText('Expired', { exact: true }).waitFor();
    assert.equal(await byID(first, `cancel-${expiring.id}`).count(), 0);
    await first.goto(`${sites.main}/carpool`);
    const firstOrder = await reserve(first, car.id);
    assert.notEqual(firstOrder.id, held.id, 'New reservation after cancellation needs a new key');
    assert.notEqual(firstOrder.id, expiring.id, 'Observed expired attempt must be retired before rebooking');
    record(`${label}: expired hold disables cancellation and can be rebooked with a new key`);
    await capture(first, `${label}-my-pending`);
    await confirmCLI(firstOrder.id);
    await refresh(first);
    await byID(first, `order-${firstOrder.id}`).getByText('Service not started').waitFor();
    const second = await context(users[2], viewport);
    const secondOrder = await reserve(second, car.id);
    await confirmCLI(secondOrder.id);
    await refresh(second);
    await byID(second, `order-${secondOrder.id}`).getByText('Service not started').waitFor();
    await capture(second, `${label}-my-waiting`);
    const full = await api(sites.main, users[0], `/admin/carpool/cars/${car.id}`);
    assert.equal(full.data.status, 'awaiting_deployment');
    assert.equal(full.data.confirmed_count, 2);
    await byID(admin, 'carpool-refresh').click();
    await rowAction(admin, 'members', car.id).click();
    await admin.getByRole('dialog').getByText('Awaiting deployment').first().waitFor();
    await capture(admin, `${label}-admin-members`);
    await admin.getByRole('button', { name: 'Close modal' }).click();
    await admin.getByRole('dialog').waitFor({ state: 'hidden' });
    await byID(admin, 'carpool-status-filter').selectOption('awaiting_deployment');
    await capture(admin, `${label}-admin-waiting`);
    record(`${label}: reserve, cancel, re-reserve, CLI confirm, full car and members agree`);

    await rowAction(admin, 'cancel', car.id).click();
    await byID(admin, 'carpool-cancel-reason').fill('Local test completed; no real payment');
    await byID(admin, 'carpool-confirm-members').waitFor();
    await byID(admin, 'carpool-cancel-confirm').click();
    const unchanged = await api(sites.main, users[0], `/admin/carpool/cars/${car.id}`);
    assert.equal(unchanged.data.status, 'awaiting_deployment', 'Missing acknowledgement must not cancel members');
    await byID(admin, 'carpool-confirm-members').check();
    const closeResponse = admin.waitForResponse(r => r.url().endsWith(`/cars/${car.id}/cancel`));
    await byID(admin, 'carpool-cancel-confirm').click();
    assert.equal((await (await closeResponse).json()).data.status, 'cancelled');
    await refresh(first);
    await byID(first, `order-${firstOrder.id}`).getByText('Cancelled', { exact: true }).first().waitFor();
    record(`${label}: confirmed-member cancellation requires explicit acknowledgement`);
  }

  // Real isolated API fixtures for the compact catalog's occupancy and layout.
  const visualCars = [];
  for (const capacity of [2, 3, 4]) {
    const draft = await api(sites.main, users[0], '/admin/carpool/cars', {
      name: `${capacity} 人车`, capacity, price_minor: 9900, currency: 'CNY', service_days: 30,
      recruit_until: new Date(Date.now() + 3600000).toISOString(), delivery_hours: 24,
      refund_policy: 'Synthetic visual test only. No real payment.'
    });
    assert.equal(draft.status, 200);
    const id = draft.data.id;
    assert.equal((await api(sites.main, users[0], `/admin/carpool/cars/${id}/publish`, {})).status, 200);
    const firstSeat = await api(sites.main, users[2], `/carpool/cars/${id}/reservations`, {}, 'POST', { 'Idempotency-Key': `visual-confirm-${capacity}` });
    assert.equal(firstSeat.status, 200);
    await confirmCLI(firstSeat.data.id);
    if (capacity !== 3) {
      const secondSeat = await api(sites.main, users[1], `/carpool/cars/${id}/reservations`, {}, 'POST', { 'Idempotency-Key': `visual-hold-${capacity}` });
      assert.equal(secondSeat.status, 200);
      if (capacity === 2) await confirmCLI(secondSeat.data.id);
    }
    visualCars.push(id);
  }
  let catalog;
  for (const language of ['zh', 'en']) {
    catalog = await context(users[1], { width: 1440, height: 1000 }, sites.main, language);
    await byID(catalog, `car-${visualCars[0]}`).waitFor();
    for (const [size, viewport, columns] of [
      ['desktop', { width: 1440, height: 1000 }, 3],
      ['tablet', { width: 1100, height: 1000 }, 2],
      ['mobile', { width: 390, height: 844 }, 1],
      ['small-mobile', { width: 320, height: 800 }, 1]
    ]) {
      await catalog.setViewportSize(viewport);
      await catalog.locator('.carpool-catalog').evaluate(element => new Promise(resolve => {
        const observer = new ResizeObserver(() => { observer.disconnect(); resolve(); });
        observer.observe(element);
      }));
      await catalog.evaluate(async () => {
        await new Promise(resolve => requestAnimationFrame(resolve));
        await Promise.all(document.getAnimations().map(animation => animation.finished.catch(() => {})));
      });
      const tracks = await catalog.locator('.carpool-catalog').evaluate(el => getComputedStyle(el).gridTemplateColumns.split(' ').length);
      assert.equal(tracks, columns, `${language} ${size} column count`);
      for (const id of visualCars) {
        const card = byID(catalog, `car-${id}`);
        assert.equal(await card.getByTestId('seat-summary').getAttribute('role'), 'img');
        const bounds = await card.boundingBox();
        layoutMeasurements.push({ language, size, car: id, width: bounds.width, height: bounds.height, columns });
        if (language === 'zh' && size === 'desktop') assert(bounds.height <= 345, `compact card height ${bounds.height}`);
        const clipped = await card.evaluate(el => [...el.querySelectorAll('h2, p, dd, button, a')]
          .filter(child => child.scrollWidth > child.clientWidth + 1)
          .map(child => ({ tag: child.tagName, text: child.textContent, width: child.clientWidth, scroll: child.scrollWidth })));
        assert.deepEqual(clipped, [], `${language} ${size}: clipped card content`);
      }
      await capture(catalog, `catalog-${language}-${size}`);
    }
    await catalog.setViewportSize({ width: 1440, height: 1000 });
    await catalog.evaluate(() => document.documentElement.classList.add('dark'));
    await capture(catalog, `catalog-${language}-dark`);
    await catalog.evaluate(() => document.documentElement.classList.remove('dark'));
    record(`compact catalog ${language}: 3/2/1 columns, 320px minimum, seat summary, height and dark mode`);
  }
  await catalog.setViewportSize({ width: 320, height: 800 });
  await catalog.route('**/api/v1/carpool/cars?*', async route => {
    const response = await route.fetch();
    const json = await response.json();
    for (const car of json.data.items) {
      car.name = 'LongUnbrokenVehicleName'.repeat(6);
      car.price_minor = 1000000000000;
      car.service_days = 3650;
      car.delivery_hours = 8760;
    }
    await route.fulfill({ response, json });
  });
  await refresh(catalog);
  await catalog.getByRole('heading', { name: 'LongUnbrokenVehicleName'.repeat(6), exact: true }).first().waitFor();
  for (const id of visualCars) {
    const card = byID(catalog, `car-${id}`);
    assert.equal(await card.evaluate(el => el.scrollWidth > el.clientWidth + 1), false);
  }
  await capture(catalog, 'catalog-long-content-small-mobile');
  record('compact catalog long names, maximum price and long periods remain contained');

  for (const name of ['disabled', 'node']) {
    const page = await context(users[1], { width: 390, height: 844 }, sites[name]);
    await page.waitForURL('**/dashboard');
    assert.equal(await page.locator('a[href="/carpool"]').count(), 0);
    assert.equal((await api(sites[name], users[1], '/carpool/cars')).status, 404);
    await page.goto(`${sites[name]}/carpool/mine`);
    await page.waitForURL('**/dashboard');
    record(`${name}: hidden navigation, direct-route denial and API 404`);
  }
  const readonly = await context(users[1], { width: 360, height: 800 }, sites.readonly);
  await readonly.getByText('Reservations unavailable').waitFor();
  for (const button of await readonly.locator('[data-testid^="reserve-"]').all()) assert(await button.isDisabled());
  await capture(readonly, 'small-mobile-readonly');
  record('main read-only: catalog available, test reservation disabled');
  const anonymous = await context(null, { width: 390, height: 844 });
  await anonymous.waitForURL('**/login?**');
  record('anonymous carpool access redirects to login');

  assert.equal(Number((await db.query('SELECT count(*) FROM carpool_node_memberships')).rows[0].count), 0);
  assert.equal(Number((await db.query('SELECT count(*) FROM carpool_memberships WHERE service_start IS NOT NULL OR service_end IS NOT NULL')).rows[0].count), 0);
  assert.equal(Number((await db.query('SELECT count(*) FROM users WHERE balance<>10')).rows[0].count), 0);
  assert.equal(errors.length, 0, `Browser errors: ${errors.join('; ')}`);
  record('no node grants, service activation, balance changes or browser exceptions');
  await writeFile(path.join(artifacts, 'results.json'), JSON.stringify({ status: 'passed', results, layoutMeasurements, limitations: ['isolated admin harness, no compliance acknowledgement', 'no payment, SMTP, production deployment or upstream calls'] }, null, 2), { mode: 0o600 });
} catch (error) {
  for (const ctx of contexts) for (const page of ctx.pages()) {
    await page.screenshot({ path: path.join(artifacts, `failure-${contexts.indexOf(ctx)}.png`), fullPage: true }).catch(() => {});
  }
  await writeFile(path.join(artifacts, 'results.json'), JSON.stringify({ status: 'failed', results, error: error.message }, null, 2), { mode: 0o600 });
  throw error;
} finally {
  await browser.close();
  await db.end();
}
