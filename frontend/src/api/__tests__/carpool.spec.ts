import { beforeEach, describe, expect, it, vi } from 'vitest'
import { carpoolAPI } from '../carpool'

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('../client', () => ({ apiClient: client }))

describe('carpool API contract', () => {
  it('reads only the signed-in member subscription and uses admin-only reconciliation', async () => {
    await carpoolAPI.nodeSubscription('car/one')
    await carpoolAPI.nativeQuota('car/one')
    await carpoolAPI.syncNativeQuota('car/one')
    expect(client.get).toHaveBeenCalledWith('/carpool/cars/car%2Fone/subscription')
    expect(client.get).toHaveBeenCalledWith('/admin/carpool/cars/car%2Fone/native/quota')
    expect(client.post).toHaveBeenCalledWith('/admin/carpool/cars/car%2Fone/native/quota/sync', {})
  })
  beforeEach(() => {
    vi.resetAllMocks()
    for (const fn of Object.values(client)) fn.mockResolvedValue({ data: { id: 'result' } })
  })
  it('reserves with the supplied idempotency key and no user-controlled identity', async () => {
    expect(await carpoolAPI.reserve('car-1', 'retry-key')).toEqual({ id: 'result' })
    expect(client.post).toHaveBeenCalledWith('/carpool/cars/car-1/reservations', {}, { headers: { 'Idempotency-Key': 'retry-key' } })
  })
  it('separates admin catalog and owned order endpoints', async () => {
    await carpoolAPI.listCars({ page: 2, page_size: 20 }, true)
    await carpoolAPI.mine({ page: 1, status: 'pending' })
    await carpoolAPI.cancelOrder('order-1')
    expect(client.get).toHaveBeenCalledWith('/admin/carpool/cars', { params: { page: 2, page_size: 20 } })
    expect(client.get).toHaveBeenCalledWith('/carpool/mine', { params: { page: 1, status: 'pending' } })
    expect(client.post).toHaveBeenCalledWith('/carpool/orders/order-1/cancel', {})
  })
  it('sends explicit cancellation acknowledgement', async () => {
    await carpoolAPI.cancelCar('car-1', 'test cancellation', true)
    expect(client.post).toHaveBeenCalledWith('/admin/carpool/cars/car-1/cancel', { reason: 'test cancellation', confirm_members: true })
    expect(carpoolAPI).not.toHaveProperty('confirmOrder')
  })
  it('sends manual commands only to admin routes with stable retry keys', async () => {
    const input = { user_id: 2, amount_minor: 9900, channel: 'alipay' as const, received_at: '2026-09-18T00:00:00Z', reference: '', note: '', confirm_received: true }
    await carpoolAPI.enrollManual('car', input, 'retry')
    expect(client.post).toHaveBeenCalledWith('/admin/carpool/cars/car/members', input, { headers: { 'Idempotency-Key': 'retry' } })
    await carpoolAPI.withdrawManual('order', { reason: 'withdraw', confirm: true }, 'withdraw-key')
    expect(client.post).toHaveBeenCalledWith('/admin/carpool/orders/order/withdraw', { reason: 'withdraw', confirm: true }, { headers: { 'Idempotency-Key': 'withdraw-key' } })
    const refund = { amount_minor: 9900, refunded_at: input.received_at, reference: '', confirm_refunded: true }
    await carpoolAPI.refundManual('order', refund, 'refund-key')
    expect(client.post).toHaveBeenCalledWith('/admin/carpool/orders/order/refund', refund, { headers: { 'Idempotency-Key': 'refund-key' } })
    await carpoolAPI.manualOrders({ refund_status: 'pending', car_id: 'car' })
    await carpoolAPI.manualOrder('order')
    expect(client.get).toHaveBeenCalledWith('/admin/carpool/orders', { params: { refund_status: 'pending', car_id: 'car' } })
    expect(client.get).toHaveBeenCalledWith('/admin/carpool/orders/order')
  })
  it('uses fixed administrator node endpoints and preserves connection generations', async () => {
    const input = { origin: 'https://node.example', node_instance_id: 'instance', admin_key: '', expected_generation: 3, disabled: false }
    await carpoolAPI.controlInfo()
    await carpoolAPI.registerNode({ car_id: 'car', hostname: 'node.example', label: 'B' })
    await carpoolAPI.nodeConnection('car/one')
    await carpoolAPI.saveNodeConnection('node/one', input)
    await carpoolAPI.checkNodeConnection('node/one')
    await carpoolAPI.nodeSync('car/one', { page: 2, page_size: 20 })
    await carpoolAPI.retryNodeSync('car/one')
    expect(client.get).toHaveBeenCalledWith('/admin/carpool/control')
    expect(client.post).toHaveBeenCalledWith('/admin/carpool/nodes', { car_id: 'car', hostname: 'node.example', label: 'B' })
    expect(client.get).toHaveBeenCalledWith('/admin/carpool/cars/car%2Fone/connection')
    expect(client.put).toHaveBeenCalledWith('/admin/carpool/nodes/node%2Fone/connection', input)
    expect(client.post).toHaveBeenCalledWith('/admin/carpool/nodes/node%2Fone/connection/check', {})
    expect(client.get).toHaveBeenCalledWith('/admin/carpool/cars/car%2Fone/sync', { params: { page: 2, page_size: 20 } })
    expect(client.post).toHaveBeenCalledWith('/admin/carpool/cars/car%2Fone/sync/retry', {})
  })
  it('scopes member key and usage operations to a car without sending user IDs', async () => {
    const query = { page: 1, page_size: 20, start_date: '2026-09-01', end_date: '2026-09-19' }
    await carpoolAPI.nodeConsole('car/one')
    await carpoolAPI.nodeKeys('car/one', { page: 1, page_size: 20 })
    await carpoolAPI.createNodeKey('car/one', 'My key', 'stable-operation')
    await carpoolAPI.updateNodeKey('car/one', 7, { status: 'disabled' })
    await carpoolAPI.deleteNodeKey('car/one', 7)
    await carpoolAPI.nodeUsage('car/one', query)
    await carpoolAPI.nodeUsageStats('car/one', query)
    expect(client.get).toHaveBeenCalledWith('/carpool/cars/car%2Fone/console')
    expect(client.get).toHaveBeenCalledWith('/carpool/cars/car%2Fone/keys', { params: { page: 1, page_size: 20 } })
    expect(client.post).toHaveBeenCalledWith('/carpool/cars/car%2Fone/keys', { name: 'My key' }, { headers: { 'Idempotency-Key': 'stable-operation' } })
    expect(client.patch).toHaveBeenCalledWith('/carpool/cars/car%2Fone/keys/7', { status: 'disabled' })
    expect(client.delete).toHaveBeenCalledWith('/carpool/cars/car%2Fone/keys/7')
    expect(client.get).toHaveBeenCalledWith('/carpool/cars/car%2Fone/usage', { params: query })
    expect(client.get).toHaveBeenCalledWith('/carpool/cars/car%2Fone/usage/stats', { params: query })
  })
})
