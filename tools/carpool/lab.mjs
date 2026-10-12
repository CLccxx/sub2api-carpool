import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { createServer as createNetServer } from 'node:net';
import { mkdtemp, mkdir, writeFile, open, appendFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import pg from 'pg';
import { chromium } from 'playwright';

const exec = promisify(execFile);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const baseline = process.env.CARPOOL_BASELINE_BINARY;
const binary = process.env.CARPOOL_TEST_BINARY || path.join(root, 'backend/bin/server-embed');
const fixture = process.env.CARPOOL_MEMBERSHIP_CLI || path.join(root, 'backend/bin/carpool-membership');
assert(baseline && path.isAbsolute(baseline), 'CARPOOL_BASELINE_BINARY must name the original embedded binary');
const runtime = await mkdtemp(path.join(os.tmpdir(), 'carpool-lab-'));
const runID = `carpool-p0-${randomBytes(4).toString('hex')}`;
const containers = [], processes = [], connections = [], results = [];
const secret = () => randomBytes(24).toString('hex');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const record = (name, detail = {}) => { results.push({ name, status: 'pass', ...detail }); console.log(`PASS ${name}`); };
let browser, upstreamCount = 0;

async function command(cmd, args, options = {}) {
  try { return (await exec(cmd, args, { maxBuffer: 8 * 1024 * 1024, ...options })).stdout.trim(); }
  catch (error) {
    await appendFile(path.join(runtime, 'commands.log'), `${cmd}: ${error.stderr || error.message}\n`, { mode: 0o600 });
    throw new Error(`${path.basename(cmd)} failed (exit ${error.code}); see private commands.log`);
  }
}
async function freePort() {
  const server = createNetServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const port = server.address().port; await new Promise(resolve => server.close(resolve)); return port;
}
async function waitFor(check, label, timeout = 120000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { try { if (await check()) return; } catch {} await delay(300); }
  throw new Error(`Timed out: ${label}`);
}
async function docker(name, args) {
  const fullName = `${runID}-${name}`;
  await command('docker', ['run', '-d', '--rm', '--name', fullName, '--label', `carpool.phase0=${runID}`, ...args]);
  containers.push(fullName); return fullName;
}
async function stop(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  await Promise.race([once(child, 'exit'), delay(15000)]);
  if (child.exitCode === null && child.signalCode === null) { child.kill('SIGKILL'); await once(child, 'exit'); }
}
const stub = createServer(async (req, res) => {
  if (req.url === '/prices' || req.url === '/prices.sha256') {
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({'gpt-4o-mini':{input_cost_per_token:0.00000015,output_cost_per_token:0.0000006,mode:'chat'}})); return;
  }
  if (req.url?.includes('/models')) {
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ object: 'list', data: [{ id: 'gpt-4o-mini', object: 'model', owned_by: 'local-fixture' }] })); return;
  }
  if (req.method !== 'POST') { res.writeHead(404); res.end(); return; }
  let raw = ''; for await (const chunk of req) raw += chunk;
  const body = JSON.parse(raw || '{}'); upstreamCount++;
  if (body.stream) {
    res.setHeader('Content-Type','text/event-stream');res.setHeader('Cache-Control','no-cache');
    const chunk = (delta, finish_reason = null) => JSON.stringify({id:'chatcmpl-local-stream',object:'chat.completion.chunk',created:Math.floor(Date.now()/1000),model:body.model,choices:[{index:0,delta,finish_reason}]});
    res.write(`data: ${chunk({role:'assistant',content:'local '})}\n\n`);
    setTimeout(()=>{res.write(`data: ${chunk({content:'fixture'},'stop')}\n\ndata: [DONE]\n\n`);res.end()},1500);
    return;
  }
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify({ id: `chatcmpl-local-${upstreamCount}`, object: 'chat.completion', created: Math.floor(Date.now()/1000), model: body.model,
    choices: [{ index: 0, message: { role: 'assistant', content: 'local carpool fixture' }, finish_reason: 'stop' }],
    usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
});

async function api(site, route, token, body, method = body === undefined ? 'GET' : 'POST') {
  const response = await fetch(`${site.local}${route}`, { method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) });
  const data = await response.json().catch(() => ({}));
  return { status: response.status, data: data.data ?? data };
}
async function launch(site, executable, nodeEnabled) {
  const logfile = await open(path.join(runtime, `${site.name}-${nodeEnabled ? 'gated' : 'original'}.log`), 'a', 0o600);
  const child = spawn(executable, [], { cwd: site.dir, env: { ...site.env, CARPOOL_NODE_ENABLED: String(nodeEnabled) }, stdio: ['ignore', logfile.fd, logfile.fd] });
  await logfile.close(); processes.push(child); site.child = child;
  await waitFor(async () => {
    if (child.exitCode !== null) throw new Error('server exited');
    return (await fetch(`${site.local}/health`)).ok;
  }, `${site.name} health`);
}
async function admin(site) {
  const r = await api(site, '/api/v1/auth/login', null, { email: site.adminEmail, password: site.adminPassword });
  assert.equal(r.status, 200, `${site.name} admin login`); assert(r.data.access_token); return r.data.access_token;
}
async function configure(site) {
  const db = new pg.Client({ host: '127.0.0.1', port: site.pgPort, user: 'carpool', password: site.dbPassword, database: 'carpool' });
  await db.connect(); connections.push(db); site.db = db;
  // Only this run's private databases are seeded. Deliberately nonzero original grants expose the baseline gap.
  for (const [key, value] of Object.entries({ registration_enabled: 'true', default_balance: '10', default_concurrency: '3', force_email_on_third_party_signup: 'false', email_verify_enabled: 'false', invitation_code_enabled: 'false', session_binding_enabled: 'false', turnstile_enabled: 'false' })) {
    await db.query('INSERT INTO settings (key,value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value', [key, value]);
  }
  // Restart clears original in-memory settings caches.
  await stop(site.child); await launch(site, baseline, false);
  site.admin = await admin(site);
  // Seed synthetic fixtures directly, without making an operator compliance acknowledgement.
  site.group = Number((await db.query("INSERT INTO groups(name,platform,rate_multiplier,is_exclusive,subscription_type,status) VALUES('Local fixture','openai',1,false,'standard','active') RETURNING id")).rows[0].id);
  const account = await db.query("INSERT INTO accounts(name,platform,type,credentials,extra,concurrency,priority,status,schedulable) VALUES('Local upstream only','openai','apikey',$1,$2,4,1,'active',true) RETURNING id", [JSON.stringify({api_key:'sk-local-fixture-not-real',base_url:upstreamURL}),JSON.stringify({openai_responses_supported:false})]);
  await db.query('INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)',[account.rows[0].id,site.group]);
  await stop(site.child);await launch(site,baseline,false);
}
async function newContext() {
  const context = await browser.newContext();
  await context.route('**/*', route => ['127.0.0.1', 'a.localhost', 'b.localhost'].includes(new URL(route.request().url()).hostname) ? route.continue() : route.abort());
  return context;
}
async function login(context, site, person, expectPrompt) {
  const page = await context.newPage();
  let callbackContainsTokens = false, callbackObserved = false, exchangeCookie = '';
  page.on('response', response => {
    if (new URL(response.url()).pathname === '/api/v1/auth/oauth/oidc/callback') {
      callbackObserved = true;
      callbackContainsTokens = /(?:access_token|refresh_token)=/.test(response.headers().location || '');
    }
  });
  page.on('request', async request => {
    if (new URL(request.url()).pathname === '/api/v1/auth/oauth/pending/exchange') exchangeCookie = (await request.allHeaders()).cookie || '';
  });
  await page.goto(`${site.base}/api/v1/auth/oauth/oidc/start?redirect=/dashboard`);
  if (expectPrompt) {
    await page.locator('#username').fill(person.username); await page.locator('#password').fill(person.password); await page.locator('#kc-login').click();
  }
  await page.waitForURL(url => url.hostname === `${site.name}.localhost` && !url.pathname.startsWith('/api/'), { timeout: 30000 });
  await page.waitForFunction(() => Boolean(localStorage.getItem('auth_token') && JSON.parse(localStorage.getItem('auth_user') || 'null')?.id), null, { timeout: 30000 });
  const auth = await page.evaluate(() => ({ token: localStorage.getItem('auth_token'), refresh: localStorage.getItem('refresh_token'), user: JSON.parse(localStorage.getItem('auth_user')) }));
  assert(auth.token && auth.user?.id, `${site.name} browser session`);
  assert(callbackObserved, 'OIDC callback response observed');
  return { ...auth, page, callbackContainsTokens, exchangeCookie };
}
async function createKey(site, token, label) {
  const result = await api(site, '/api/v1/keys', token, { name: label, group_id: site.group });
  assert.equal(result.status, 200, `create key ${label}: ${result.data.message || result.data.reason || ''}`); assert(result.data.key); return result.data;
}
async function model(site, key, status = 200) {
  const result = await api(site, '/v1/chat/completions', key, { model: 'gpt-4o-mini', messages: [{ role: 'user', content: 'local test' }], stream: false });
  assert.equal(result.status, status, `model request: ${JSON.stringify(result.data).slice(0, 400)}`);
  if (status === 200) assert.equal(result.data.choices?.[0]?.message?.content, 'local carpool fixture');
  return result;
}
async function apply(site, grant, expected = 0) {
  const child = spawn(fixture, [], { env: { ...process.env, CARPOOL_NODE_ID: 'node-b', CARPOOL_CAR_ID: 'car-1', CARPOOL_ISSUER: issuer, CARPOOL_DATABASE_DSN: `postgres://carpool:${site.dbPassword}@127.0.0.1:${site.pgPort}/carpool?sslmode=disable` }, stdio: ['pipe','pipe','pipe'] });
  child.stdout.resume(); child.stderr.resume(); child.stdin.end(JSON.stringify(grant)); const [code] = await once(child, 'exit'); assert.equal(code, expected, 'membership fixture exit');
}
function grant(person, version, status = 'active', overrides = {}) {
  const now = Date.now(); return { node_id: 'node-b', car_id: 'car-1', issuer, subject: person.id, version, status,
    valid_from: new Date(now-60000).toISOString(), valid_until: new Date(now+3600000).toISOString(), lease_until: new Date(now+14*60000).toISOString(), ...overrides };
}

const idpPort = await freePort(), upstreamPort = await freePort();
const issuer = `http://127.0.0.1:${idpPort}/realms/carpool`;
const upstreamURL = `http://127.0.0.1:${upstreamPort}`;
const people = Object.fromEntries(['alice','bob','carol','outsider'].map(username => [username, { id: randomUUID(), username, password: secret(), email: `${username}@carpool.test` }]));
const sites = [];
let failure;
try {
  console.log(`Private lab runtime: ${runtime}`);
  stub.listen(upstreamPort, '127.0.0.1'); await once(stub, 'listening');
  for (const name of ['a','b']) {
    const port = await freePort(), pgPort = await freePort(), redisPort = await freePort();
    const site = { name, port, pgPort, redisPort, dbPassword: secret(), redisPassword: secret(), clientSecret: secret(), adminEmail: `admin-${name}@carpool.test`, adminPassword: secret(), dir: path.join(runtime, name), local: `http://127.0.0.1:${port}`, base: `http://${name}.localhost:${port}` };
    await mkdir(site.dir, { mode: 0o700 }); sites.push(site);
    await docker(`pg-${name}`, ['-p', `127.0.0.1:${pgPort}:5432`, '-e', 'POSTGRES_USER=carpool', '-e', `POSTGRES_PASSWORD=${site.dbPassword}`, '-e', 'POSTGRES_DB=carpool', 'postgres:18.1-alpine3.23']);
    await docker(`redis-${name}`, ['-p', `127.0.0.1:${redisPort}:6379`, 'redis:8.4-alpine', 'redis-server', '--requirepass', site.redisPassword, '--save', '', '--appendonly', 'no']);
    site.env = { ...process.env, AUTO_SETUP:'true', DATA_DIR:site.dir, DATABASE_HOST:'127.0.0.1', DATABASE_PORT:String(pgPort), DATABASE_USER:'carpool', DATABASE_PASSWORD:site.dbPassword, DATABASE_DBNAME:'carpool', DATABASE_SSLMODE:'disable', REDIS_HOST:'127.0.0.1', REDIS_PORT:String(redisPort), REDIS_PASSWORD:site.redisPassword, REDIS_DB:'0', SERVER_HOST:'127.0.0.1', SERVER_PORT:String(port), SERVER_MODE:'debug', SERVER_FRONTEND_URL:site.base, ADMIN_EMAIL:site.adminEmail, ADMIN_PASSWORD:site.adminPassword, JWT_SECRET:secret(), TOTP_ENCRYPTION_KEY:randomBytes(32).toString('hex'), TZ:'UTC', RUN_MODE:'standard',
      OIDC_CONNECT_ENABLED:'true', OIDC_CONNECT_CLIENT_ID:`carpool-${name}`, OIDC_CONNECT_CLIENT_SECRET:site.clientSecret, OIDC_CONNECT_ISSUER_URL:issuer, OIDC_CONNECT_REDIRECT_URL:`${site.base}/api/v1/auth/oauth/oidc/callback`, OIDC_CONNECT_FRONTEND_REDIRECT_URL:'/auth/oidc/callback', OIDC_CONNECT_USE_PKCE:'true', OIDC_CONNECT_VALIDATE_ID_TOKEN:'true', OIDC_CONNECT_REQUIRE_EMAIL_VERIFIED:'true', OIDC_CONNECT_CLOCK_SKEW_SECONDS:'5',
      CARPOOL_NODE_NODE_ID:'node-b', CARPOOL_NODE_CAR_ID:'car-1', CARPOOL_NODE_ISSUER:issuer, PRICING_REMOTE_URL:`${upstreamURL}/prices`, PRICING_HASH_URL:`${upstreamURL}/prices.sha256`, PRICING_DATA_DIR:path.join(site.dir,'pricing'), BATCH_IMAGE_ENABLED:'false', BATCH_IMAGE_VERTEX_ENABLED:'false', HTTP_PROXY:'', HTTPS_PROXY:'', ALL_PROXY:'', NO_PROXY:'*' };
  }
  const realm = { realm:'carpool', enabled:true, sslRequired:'none', registrationAllowed:false, accessTokenLifespan:300,
    clients:sites.map(site => ({ clientId:`carpool-${site.name}`, enabled:true, protocol:'openid-connect', publicClient:false, secret:site.clientSecret, standardFlowEnabled:true, directAccessGrantsEnabled:false, redirectUris:[`${site.base}/api/v1/auth/oauth/oidc/callback`], webOrigins:[site.base], attributes:{ 'pkce.code.challenge.method':'S256' } })),
    users:Object.values(people).map(person => ({ id:person.id, username:person.username, email:person.email, emailVerified:true, enabled:true, firstName:person.username, lastName:'Fixture', credentials:[{ type:'password', value:person.password, temporary:false }] })) };
  // Container user must read this file, but its enclosing host directory is private.
  const realmPath = path.join(runtime,'realm.json'); await writeFile(realmPath, JSON.stringify(realm), { mode:0o644 });
  const idp = await docker('idp',['-p',`127.0.0.1:${idpPort}:8080`,'-v',`${realmPath}:/opt/keycloak/data/import/carpool.json:ro`,'quay.io/keycloak/keycloak:26.7.3','start-dev','--import-realm','--hostname',`http://127.0.0.1:${idpPort}`]);
  await waitFor(async () => (await fetch(`${issuer}/.well-known/openid-configuration`)).ok,'Keycloak',180000);
  for (const site of sites) { await launch(site,baseline,false); await configure(site); }
  browser = await chromium.launch({ headless:true, channel:process.env.CARPOOL_BROWSER_CHANNEL || 'chrome', args:['--host-resolver-rules=MAP a.localhost 127.0.0.1,MAP b.localhost 127.0.0.1'] });
  const [a,b] = sites;
  const aliceContext = await newContext();
  const aliceA = await login(aliceContext,a,people.alice,true);
  const aliceB = await login(aliceContext,b,people.alice,false);
  record('original A-to-B browser SSO without second password');
  const identity = async site => (await site.db.query("SELECT a.provider_key,a.provider_subject FROM auth_identities a JOIN users u ON u.id=a.user_id WHERE a.provider_type='oidc' AND u.email=$1",[people.alice.email])).rows[0];
  assert.deepEqual(await identity(a),await identity(b));assert.equal((await identity(b)).provider_subject,people.alice.id);
  record('same issuer/subject maps into independent local users');
  const bobContext = await newContext(); const bobB = await login(bobContext,b,people.bob,true);
  const originalBobKey = await createKey(b,bobB.token,'original-unentitled-bob'); await model(b,originalBobKey.key);
  record('original authorization gap reproduced',{ unentitledKeyCreated:true, upstreamReached:true });
  const aKey = await createKey(a,aliceA.token,'alice-a');
  const bKey = await createKey(b,aliceB.token,'alice-b');
  for (const [site,token] of [[a,aliceB.token],[b,aliceA.token]]) assert.equal((await api(site,'/api/v1/user/profile',token)).status,401);
  for (const [site,key] of [[a,bKey.key],[b,aKey.key]]) await model(site,key,401);
  record('independent A/B JWT signatures and API keys');
  await stop(b.child); await launch(b,binary,true);
  assert.equal((await api(b,'/api/v1/keys',aliceB.token,{name:'not-yet-authorized'})).status,403);
  await model(b,bKey.key,403); await model(b,originalBobKey.key,403);
  record('node defaults deny old sessions and previously valid keys');
  const outsiders = await newContext(); const outsiderPage = await outsiders.newPage();
  await outsiderPage.goto(`${b.base}/api/v1/auth/oauth/oidc/start`);
  await outsiderPage.locator('#username').fill(people.outsider.username);await outsiderPage.locator('#password').fill(people.outsider.password);await outsiderPage.locator('#kc-login').click();
  await outsiderPage.waitForURL(url=>url.hostname==='b.localhost' && url.hash.includes('node_access_denied'));
  assert.equal((await b.db.query('SELECT count(*)::int AS n FROM users WHERE email=$1',[people.outsider.email])).rows[0].n,0);
  await outsiderPage.getByText('NODE_MEMBERSHIP_REQUIRED', {exact:true}).waitFor({state:'visible'});
  await outsiderPage.screenshot({path:path.join(runtime,'unauthorized.png'),animations:'disabled'});record('unentitled OIDC identity rejected before account creation');
  const firstGrant = grant(people.alice,1);await apply(b,firstGrant);await apply(b,firstGrant);
  await model(b,bKey.key);await model(b,bKey.key);
  record('active grant and idempotent replay; B reaches only local upstream');
  const protectedContext = await newContext(); const protectedLogin = await login(protectedContext,b,people.alice,true);
  const grantedKey = await createKey(b,protectedLogin.token,'created-with-active-membership');await model(b,grantedKey.key);
  assert.equal(protectedLogin.callbackContainsTokens,false);assert(protectedLogin.exchangeCookie,'capture consumed browser-bound session for replay');
  const replay = await protectedContext.request.post(`${b.base}/api/v1/auth/oauth/pending/exchange`,{data:{},headers:{Cookie:protectedLogin.exchangeCookie}});assert.notEqual(replay.status(),200);
  record('token-free callback and single-use pending exchange');
  assert.equal((await api(b,'/api/v1/admin/users',protectedLogin.token)).status,403);
  await apply(b,grant(people.carol,1));const carolContext = await newContext();const carolB = await login(carolContext,b,people.carol,true);
  assert.equal(Number((await b.db.query('SELECT balance FROM users WHERE id=$1',[carolB.user.id])).rows[0].balance),0);
  assert.equal((await api(b,`/api/v1/keys/${bKey.id}`,carolB.token)).status,404);
  record('no signup credit; member cannot access admin or another user key');
  const before = upstreamCount;await apply(b,grant(people.alice,2,'revoked'));
  await model(b,bKey.key,403);assert.equal((await api(b,'/api/v1/keys',protectedLogin.token,{name:'revoked'})).status,403);
  const refresh = await api(b,'/api/v1/auth/refresh',null,{refresh_token:protectedLogin.refresh});assert.notEqual(refresh.status,200);
  assert.equal(upstreamCount,before);await apply(b,firstGrant,1);await model(b,bKey.key,403);
  record('warmed-cache revoke blocks old key, session, refresh and stale regrant',{ deniedUpstreamRequests:0 });
  const deniedRoutes = ['/v1/responses','/v1/messages','/v1/images/generations','/v1beta/models/gemini-2.0-flash:generateContent'];
  for(const route of deniedRoutes){const response=await api(b,route,bKey.key,{model:'gpt-4o-mini'});assert.equal(response.status,403,route)}
  assert.equal(upstreamCount,before);record('revoked key blocked across OpenAI, Anthropic, image and Google ingress');
  for(const [version,status,overrides] of [[3,'paused',{}],[4,'active',{valid_until:new Date(Date.now()-1000).toISOString(),lease_until:new Date(Date.now()-2000).toISOString()}],[5,'active',{lease_until:new Date(Date.now()-1000).toISOString()}]]){
    await apply(b,grant(people.alice,version,status,overrides));await model(b,bKey.key,403);
  }
  assert.equal(upstreamCount,before);record('pause, service expiry and lease expiry fail closed');
  await apply(b,grant(people.alice,6));await model(b,bKey.key);
  await protectedLogin.page.goto(`${b.base}/dashboard`);
  await protectedLogin.page.locator('main .card').first().waitFor({state:'visible'});
  await protectedLogin.page.screenshot({path:path.join(runtime,'authorized.png'),animations:'disabled'});
  record('newer valid grant restores access');
  const liveStream = await fetch(`${b.local}/v1/chat/completions`,{method:'POST',headers:{'Content-Type':'application/json',Authorization:`Bearer ${bKey.key}`},body:JSON.stringify({model:'gpt-4o-mini',messages:[{role:'user',content:'local streaming fixture'}],stream:true})});
  assert.equal(liveStream.status,200);const reader=liveStream.body.getReader();const firstChunk=await reader.read();assert(!firstChunk.done);
  const streamCount=upstreamCount;await apply(b,grant(people.alice,7,'revoked'));await model(b,bKey.key,403);
  let streamBody=new TextDecoder().decode(firstChunk.value);for(;;){const next=await reader.read();if(next.done)break;streamBody+=new TextDecoder().decode(next.value)}
  assert(streamBody.includes('[DONE]'));assert.equal(upstreamCount,streamCount);
  record('in-flight stream completes while post-revocation requests are denied');
  const shortLease=grant(people.alice,8,'active',{lease_until:new Date(Date.now()+2500).toISOString()});await apply(b,shortLease);await model(b,bKey.key);
  const leaseCount=upstreamCount;await delay(Math.max(0,Date.parse(shortLease.lease_until)-Date.now()+100));await model(b,bKey.key,403);assert.equal(upstreamCount,leaseCount);
  record('lease expires without any new event or cache invalidation');
  await stop(a.child);await launch(a,binary,false);
  assert.equal((await api(a,'/api/v1/user/profile',aliceA.token)).status,200);await model(a,aKey.key);
  const defaultContext=await newContext();const defaultLogin=await login(defaultContext,a,people.outsider,true);
  assert.equal(defaultLogin.callbackContainsTokens,false);
  assert.equal(Number((await a.db.query('SELECT balance FROM users WHERE id=$1',[defaultLogin.user.id])).rows[0].balance),10);
  const defaultKey=await createKey(a,defaultLogin.token,'modified-default-a');await model(a,defaultKey.key);
  record('modified default A preserves existing sessions, keys and OIDC signup grants');
  await writeFile(path.join(runtime,'idp.log'),await command('docker',['logs',idp]),{mode:0o600});
} catch(error) {
  failure=error;console.error(error.message);await writeFile(path.join(runtime,'failure.txt'),error.stack,{mode:0o600});
} finally {
  if(browser)await browser.close();
  for(const child of processes.reverse())await stop(child);
  for(const db of connections)await db.end();
  await new Promise(resolve=>stub.close(resolve));
  for(const name of containers.reverse())await command('docker',['rm','-f',name]).catch(()=>{});
  await writeFile(path.join(runtime,'results.json'),JSON.stringify({runID,baseline,binary,status:failure?'failed':'passed',results,limitations:['local HTTP only','manual grants, not main-site synchronization','no C-node, real upstream, payment, SMTP or egress verification']},null,2),{mode:0o600});
  console.log(`Results: ${path.join(runtime,'results.json')}`);
}
if(failure)process.exitCode=1;
