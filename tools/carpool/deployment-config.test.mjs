import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const fixture = {
  PATH: process.env.PATH,
  HOME: process.env.HOME,
  CARPOOL_IMAGE: `sha256:${'a'.repeat(64)}`,
  PUBLIC_URL: 'https://synthetic.example.invalid',
  BIND_HOST: '127.0.0.1',
  SERVER_TRUSTED_PROXIES: '172.30.0.1/32',
  POSTGRES_PASSWORD: 'synthetic-database-only',
  REDIS_PASSWORD: 'synthetic-redis-only',
  ADMIN_EMAIL: 'synthetic@example.invalid',
  ADMIN_PASSWORD: 'synthetic-administrator-only',
  JWT_SECRET: '1'.repeat(64),
  TOTP_ENCRYPTION_KEY: '2'.repeat(64),
  CARPOOL_CONTROL_ENCRYPTION_KEY: Buffer.alloc(32, 3).toString('base64'),
  CARPOOL_CONTROL_ALLOWED_ORIGINS: 'https://b.example.invalid,https://c.example.invalid',
  CARPOOL_NODE_MAIN_INSTANCE_ID: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
  CARPOOL_NODE_NODE_ID: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
  CARPOOL_NODE_CAR_ID: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
}

function compose(role, omitted) {
  const env = { ...fixture }
  if (omitted) delete env[omitted]
  return spawnSync('docker', ['compose', '--env-file', '/dev/null', '-f', 'deploy/docker-compose.local.yml', '-f', `deploy/carpool/${role}.compose.yml`, 'config', '--format', 'json'], { cwd: root, env, encoding: 'utf8' })
}

for (const role of ['main', 'node']) {
  test(`${role} overlay uses the supplied image ID and private ports`, () => {
    const result = compose(role)
    assert.equal(result.status, 0, result.stderr)
    const config = JSON.parse(result.stdout)
    const app = config.services.sub2api
    assert.equal(app.image, fixture.CARPOOL_IMAGE)
    assert.equal(app.pull_policy, 'never')
    assert.equal(app.ports[0].host_ip, '127.0.0.1')
    assert.equal(app.environment.RUN_MODE, 'standard')
    assert.equal(app.environment.SECURITY_TRUST_FORWARDED_IP_FOR_API_KEY_ACL, 'false')
    assert.equal(app.environment.SERVER_TRUSTED_PROXIES, fixture.SERVER_TRUSTED_PROXIES)
    assert.equal(app.environment.SERVER_FRONTEND_URL, fixture.PUBLIC_URL)
    assert.equal(app.environment.CARPOOL_MAIN_ENABLED, String(role === 'main'))
    assert.equal(app.environment.CARPOOL_NODE_ENABLED, String(role === 'node'))
    assert.equal(app.environment.CARPOOL_CONTROL_ENABLED, String(role === 'main'))
    assert.equal(config.services.postgres.image, 'postgres:18.1-alpine3.23')
    assert.equal(config.services.redis.image, 'redis:8.4-alpine')
    assert.equal(config.services.postgres.ports, undefined)
    assert.equal(config.services.redis.ports, undefined)
    if (role === 'main') {
      assert.equal(app.environment.CARPOOL_MAIN_MANUAL_ENROLLMENT_ENABLED, 'true')
      assert.equal(app.environment.CARPOOL_MAIN_TEST_ORDERS_ENABLED, 'false')
      assert.equal(app.environment.CARPOOL_CONTROL_ENCRYPTION_KEY, fixture.CARPOOL_CONTROL_ENCRYPTION_KEY)
    } else {
      assert.equal(app.environment.CARPOOL_NODE_AUTHORIZATION_MODE, 'managed')
      assert.equal(app.environment.CARPOOL_NODE_GROUP_ID, '0')
      assert.equal(app.environment.OIDC_CONNECT_ENABLED, 'false')
      assert.equal(app.environment.CARPOOL_NODE_CAR_ID, fixture.CARPOOL_NODE_CAR_ID)
    }
  })
  test(`${role} explicitly trusts no proxy when the list is omitted`, () => {
    const result = compose(role, 'SERVER_TRUSTED_PROXIES')
    assert.equal(result.status, 0, result.stderr)
    const env = JSON.parse(result.stdout).services.sub2api.environment
    assert.equal(env.SECURITY_TRUST_FORWARDED_IP_FOR_API_KEY_ACL, 'false')
    assert.equal(env.SERVER_TRUSTED_PROXIES, '')
  })
  for (const required of ['CARPOOL_IMAGE', 'PUBLIC_URL', 'JWT_SECRET', 'TOTP_ENCRYPTION_KEY', role === 'main' ? 'CARPOOL_CONTROL_ENCRYPTION_KEY' : 'CARPOOL_NODE_CAR_ID']) {
    test(`${role} refuses missing ${required}`, () => {
      const result = compose(role, required)
      assert.notEqual(result.status, 0)
      assert.match(result.stderr, new RegExp(required))
    })
  }
}
