import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { carpoolEn } from '@/i18n/locales/carpool'
import { reservationKey, rememberReservationOrder } from '@/utils/carpool'
import Lobby from '../CarpoolLobbyView.vue'
import Mine from '../CarpoolMineView.vue'

const api = vi.hoisted(() => ({ listCars: vi.fn(), mine: vi.fn(), reserve: vi.fn(), cancelOrder: vi.fn(), nodeConsole: vi.fn() }))
const app = vi.hoisted(() => ({ cachedPublicSettings: { carpool_main_enabled: true, carpool_control_enabled: false, carpool_test_orders_enabled: true, carpool_manual_enrollment_enabled: false, contact_info: '' }, showSuccess: vi.fn() }))
const auth = vi.hoisted(() => ({ user: { id: 12 } }))
const push = vi.hoisted(() => vi.fn())
vi.mock('@/api/carpool', () => ({ carpoolAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => app }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push }) }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ locale: { value: 'en' }, t: (key: string, params: Record<string, unknown> = {}) => {
  const text = key.replace(/^carpool\./, '').split('.').reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], carpoolEn)
  return typeof text === 'string' ? text.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? '')) : key
} }) }))
const now = '2026-09-16T12:00:00Z'
const terms = { name: 'Local test car', capacity: 2, price_minor: 9900, currency: 'CNY', service_days: 30, recruit_until: '2026-09-16T13:00:00Z', delivery_hours: 24, refund_policy: 'Synthetic rules' }
const car = { ...terms, id: 'car-1', status: 'recruiting', confirmed_count: 0, held_count: 0, available_count: 2, reservable: true, server_time: now, created_at: now }
const order = { id: 'order-1', car_id: 'car-1', user_id: 12, seat_no: 1, mode: 'test', status: 'pending', snapshot: terms, expires_at: '2026-09-16T12:10:00Z', created_at: now, membership: null }
const page = (items: unknown[]) => ({ items, total: items.length, page: 1, page_size: 20, server_time: now })
let wrapper: VueWrapper | undefined
function render(component: typeof Lobby | typeof Mine) {
  wrapper = mount(component, { global: {
    stubs: { AppLayout: { template: '<main><slot /></main>' }, RouterLink: { template: '<a><slot /></a>' }, Pagination: true, teleport: true }
  } })
  return wrapper
}
beforeEach(() => {
  vi.resetAllMocks()
  sessionStorage.clear()
  app.cachedPublicSettings.carpool_test_orders_enabled = true
  app.cachedPublicSettings.carpool_manual_enrollment_enabled = false
  app.cachedPublicSettings.carpool_control_enabled = false
  app.cachedPublicSettings.contact_info = ''
  api.listCars.mockResolvedValue(page([car]))
  api.mine.mockResolvedValue(page([]))
  api.reserve.mockResolvedValue(order)
  api.cancelOrder.mockResolvedValue({ ...order, status: 'cancelled' })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

describe('member node console entry', () => {
  it('is opt-in and opens the selected membership without logging into a node', async () => {
    api.mine.mockResolvedValue(page([{ ...order, status: 'confirmed', membership: { id: 'membership', status: 'waiting_deployment', service_start: null, service_end: null } }]))
    let view = render(Mine); await flushPromises()
    expect(view.find('[data-testid="console-order-1"]').exists()).toBe(false)
    view.unmount(); app.cachedPublicSettings.carpool_control_enabled = true
    api.nodeConsole.mockResolvedValue({ car_id: 'car-1', access_status: 'unactivated', can_create_key: false, can_read_node: false, server_time: now, service_start: null, service_end: null })
    view = render(Mine); await flushPromises()
    await view.get('[data-testid="console-order-1"]').trigger('click'); await flushPromises()
    expect(api.nodeConsole).toHaveBeenCalledWith('car-1')
    expect(view.get('[data-testid="console-create"]').attributes('disabled')).toBeDefined()
    expect(push).not.toHaveBeenCalled()
  })
  it('renders an active service window instead of the unactivated message', async () => {
    api.mine.mockResolvedValue(page([{ ...order, status: 'confirmed', membership: { id: 'membership', status: 'active', service_start: now, service_end: '2026-10-16T12:00:00Z' } }]))
    const view = render(Mine); await flushPromises()
    expect(view.text()).not.toContain('Service not started')
    expect(view.text()).toContain('Active')
    expect(view.text()).toContain('Oct 16, 2026')
  })
})

describe('Carpool lobby', () => {
  it('shows escaped contact details and no reservation for a manual car', async () => {
    app.cachedPublicSettings.carpool_test_orders_enabled = false
    app.cachedPublicSettings.carpool_manual_enrollment_enabled = true
    app.cachedPublicSettings.contact_info = '<img src=x onerror=alert(1)> Contact admin'
    api.listCars.mockResolvedValue(page([{ ...car, enrollment_mode: 'manual', reservable: false }]))
    const view = render(Lobby); await flushPromises()
    expect(view.find('[data-testid="reserve-car-1"]').exists()).toBe(false)
    expect(view.text()).not.toContain('Test orders')
    await view.get('[data-testid="contact-car-1"]').trigger('click')
    expect(view.text()).toContain(app.cachedPublicSettings.contact_info)
    expect(view.find('img[src="x"]').exists()).toBe(false)
  })
  it.each(['needs_review', 'awaiting_deployment', 'closed_unfilled', 'cancelled'])('does not invite contact for %s manual cars', async status => {
    app.cachedPublicSettings.carpool_manual_enrollment_enabled = true
    app.cachedPublicSettings.carpool_test_orders_enabled = false
    app.cachedPublicSettings.contact_info = 'admin contact'
    api.listCars.mockResolvedValue(page([{ ...car, enrollment_mode: 'manual', status }]))
    const view = render(Lobby); await flushPromises()
    expect(view.find('[data-testid="reserve-car-1"]').exists()).toBe(false)
    expect(view.find('[data-testid="contact-car-1"]').exists()).toBe(false)
  })
  it.each([true, false])('does not show manual contact with missing contact details (additions enabled: %s)', async enabled => {
    app.cachedPublicSettings.carpool_manual_enrollment_enabled = enabled
    app.cachedPublicSettings.carpool_test_orders_enabled = false
    api.listCars.mockResolvedValue(page([{ ...car, enrollment_mode: 'manual' }]))
    const view = render(Lobby); await flushPromises()
    expect(view.find('[data-testid="contact-car-1"]').exists()).toBe(false)
    expect(view.find('[data-testid="reserve-car-1"]').exists()).toBe(false)
  })
  it('does not invite enrollment when manual additions are disabled even with contact details', async () => {
    app.cachedPublicSettings.carpool_test_orders_enabled = false
    app.cachedPublicSettings.contact_info = 'admin contact'
    api.listCars.mockResolvedValue(page([{ ...car, enrollment_mode: 'manual' }]))
    const view = render(Lobby); await flushPromises()
    expect(view.find('[data-testid="contact-car-1"]').exists()).toBe(false)
    expect(view.find('[data-testid="reserve-car-1"]').exists()).toBe(false)
  })
  it.each([2, 3, 4])('shows confirmed/total occupancy and separate available seats for %i seats', async (capacity) => {
    api.listCars.mockResolvedValue(page([{ ...car, capacity, confirmed_count: 1, held_count: 1, available_count: capacity - 2 }]))
    const view = render(Lobby)
    await flushPromises()
    const card = view.get('[data-testid="car-car-1"]')
    expect(card.get('[data-testid="seat-availability"]').text().replace(/\s/g, '')).toBe(`1/${capacity}`)
    expect(card.get('[data-testid="seats-remaining"]').text()).toContain(String(capacity - 2))
    const seats = card.get('[data-testid="seat-summary"]')
    expect(seats.attributes('role')).toBe('img')
    expect(seats.attributes('aria-label')).toBe(`1 confirmed, 1 reserved, ${capacity - 2} available of ${capacity} seats`)
    expect(seats.findAll('[data-seat-state="confirmed"]')).toHaveLength(1)
    expect(seats.findAll('[data-seat-state="held"]')).toHaveLength(1)
    expect(seats.findAll('[data-seat-state="available"]')).toHaveLength(capacity - 2)
    expect(card.get('[data-testid="seat-price"]').text()).toContain('99.00')
  })
  it('does not present fully held seats as confirmed or awaiting deployment', async () => {
    api.listCars.mockResolvedValue(page([{ ...car, held_count: 2, available_count: 0 }]))
    const view = render(Lobby)
    await flushPromises()
    expect(view.get('[data-testid="seat-availability"]').text()).toContain('0')
    expect(view.findAll('[data-seat-state="held"]')).toHaveLength(2)
    expect(view.get('[data-testid="car-car-1"]').text()).not.toContain('Full · awaiting deployment')
    expect(view.get('[data-testid="reserve-car-1"]').attributes('disabled')).toBeDefined()
  })
  it.each(['cancelled', 'closed_unfilled'])('does not advertise remaining capacity as open when %s', async (status) => {
    api.listCars.mockResolvedValue(page([{ ...car, status }]))
    const view = render(Lobby)
    await flushPromises()
    expect(view.get('[data-testid="seat-availability"]').text()).toBe('Recruitment closed')
    expect(view.get('[data-testid="reserve-car-1"]').attributes('disabled')).toBeDefined()
  })
  it('keeps creation time and full terms in details without expanding the card', async () => {
    const view = render(Lobby)
    await flushPromises()
    const card = view.get('[data-testid="car-car-1"]')
    expect(card.text()).not.toContain('Created')
    await card.findAll('button').find(button => button.text() === 'Details')!.trigger('click')
    expect(view.get('[role="dialog"]').text()).toContain('Created')
    expect(view.get('[role="dialog"]').text()).toContain('Synthetic rules')
  })
  it('requires confirmation and reuses a retry key after a network failure', async () => {
    api.reserve.mockRejectedValueOnce({ status: 0 })
    const view = render(Lobby)
    await flushPromises()
    await view.get('[data-testid="reserve-car-1"]').trigger('click')
    expect(api.reserve).not.toHaveBeenCalled()
    await view.get('[data-testid="confirm-reserve"]').trigger('click')
    await flushPromises()
    expect(view.text()).toContain('Request failed')
    await view.get('[data-testid="confirm-reserve"]').trigger('click')
    await flushPromises()
    expect(api.reserve).toHaveBeenCalledTimes(2)
    expect(api.reserve.mock.calls[1]).toEqual(api.reserve.mock.calls[0])
    expect(push).toHaveBeenCalledWith('/carpool/mine')
  })
  it('disables booking when test orders are off or capacity is full', async () => {
    app.cachedPublicSettings.carpool_test_orders_enabled = false
    const view = render(Lobby)
    await flushPromises()
    expect(view.get('[data-testid="reserve-car-1"]').attributes('disabled')).toBeDefined()
  })
  it.each([
    { available_count: 0 },
    { reservable: false },
    { status: 'awaiting_deployment' },
    { recruit_until: now }
  ])('does not book full, nonreservable, waiting or deadline-passed cars (%j)', async (change) => {
    api.listCars.mockResolvedValue(page([{ ...car, ...change }]))
    const view = render(Lobby)
    await flushPromises()
    expect(view.get('[data-testid="reserve-car-1"]').attributes('disabled')).toBeDefined()
  })
  it('reads all live-order pages so older participation is not missed', async () => {
    api.mine.mockImplementation(({ status, page: number }) => Promise.resolve(status !== 'pending' ? page([]) : {
      ...page(number === 1 ? [{ ...order, car_id: 'another-car' }] : [order]), total: 2, page_size: 1, page: number
    }))
    const view = render(Lobby)
    await flushPromises()
    expect(api.mine).toHaveBeenCalledWith({ page: 2, page_size: 100, status: 'pending' })
    expect(view.find('[data-testid="reserve-car-1"]').exists()).toBe(false)
  })
  it('shows the server delivery deadline for a full car', async () => {
    api.listCars.mockResolvedValue(page([{ ...car, status: 'awaiting_deployment', delivery_due: '2026-09-17T12:00:00Z' }]))
    const view = render(Lobby)
    await flushPromises()
    expect(view.text()).toContain('Delivery due')
  })
  it('shows current participation instead of another reserve action', async () => {
    api.mine.mockImplementation(({ status }) => Promise.resolve(page(status === 'pending' ? [order] : [])))
    const view = render(Lobby)
    await flushPromises()
    expect(view.text()).toContain('View my order')
    expect(view.find('[data-testid="reserve-car-1"]').exists()).toBe(false)
  })
  it('does not allow a reservation when own-state loading fails', async () => {
    api.mine.mockRejectedValue({ status: 503 })
    const view = render(Lobby)
    await flushPromises()
    expect(view.find('[data-testid="reserve-car-1"]').exists()).toBe(false)
    expect(view.find('[role="alert"]').exists()).toBe(true)
  })
  it('refreshes after conflicts without displaying success', async () => {
    api.reserve.mockRejectedValue({ status: 409 })
    const view = render(Lobby)
    await flushPromises()
    await view.get('[data-testid="reserve-car-1"]').trigger('click')
    await view.get('[data-testid="confirm-reserve"]').trigger('click')
    await flushPromises()
    expect(api.listCars).toHaveBeenCalledTimes(2)
    expect(view.text()).toContain('The seat or order changed')
    expect(app.showSuccess).not.toHaveBeenCalled()
  })
})

describe('My cars', () => {
  it.each(['pending', 'refunded'])('shows manual %s refund after additions are disabled without test controls', async status => {
    api.mine.mockResolvedValue(page([{ ...order, mode: 'manual', status: 'cancelled', expires_at: null,
      payment: { amount_minor: 9900, currency: 'CNY', channel: 'wechat', received_at: now, recorded_at: now, refund: { status, amount_minor: 9900, requested_at: now, refunded_at: status === 'refunded' ? now : null } } }]))
    const view = render(Mine); await flushPromises()
    expect(view.text()).toContain(status === 'pending' ? 'Refund pending' : 'Refund recorded')
    expect(view.get('[data-testid="order-order-1"]').text()).not.toContain('Test confirmed')
    expect(view.find('[data-testid="cancel-order-1"]').exists()).toBe(false)
    expect(view.text()).not.toContain('10:00')
  })
  it('renders test snapshots/countdown and cancels a pending order', async () => {
    api.mine.mockResolvedValue(page([order]))
    const view = render(Mine)
    await flushPromises()
    expect(view.text()).toContain('Test orders')
    expect(view.text()).toContain('10:00')
    await view.get('[data-testid="cancel-order-1"]').trigger('click')
    await view.get('[data-testid="confirm-cancel-order"]').trigger('click')
    await flushPromises()
    expect(api.cancelOrder).toHaveBeenCalledWith('order-1')
    expect(api.mine).toHaveBeenCalledTimes(2)
  })
  it('shows awaiting deployment, never an active node or payment control', async () => {
    api.mine.mockResolvedValue(page([{ ...order, status: 'confirmed', membership: { status: 'waiting_deployment', service_start: null, service_end: null } }]))
    const view = render(Mine)
    await flushPromises()
    expect(view.text()).toContain('Service not started')
    expect(view.find('[data-testid="cancel-order-1"]').exists()).toBe(false)
    expect(view.find('a[href*="node"]').exists()).toBe(false)
  })
  it('has an empty state', async () => {
    const view = render(Mine)
    await flushPromises()
    expect(view.text()).toContain('No orders yet')
  })
  it('uses the server time even when the device clock is incorrect', async () => {
    api.mine.mockResolvedValue(page([order]))
    const clock = vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2000-01-01T00:00:00Z'))
    try {
      const view = render(Mine)
      await flushPromises()
      expect(view.text()).toContain('10:00')
    } finally { clock.mockRestore() }
  })
  it('does not allow cancelling a locally expired hold', async () => {
    api.mine.mockResolvedValue(page([{ ...order, expires_at: now }]))
    const view = render(Mine)
    await flushPromises()
    expect(view.find('[data-testid="cancel-order-1"]').exists()).toBe(false)
    expect(view.text()).toContain('Expired')
  })
  it('retires the matching saved attempt when the API reports a terminal order', async () => {
    const key = reservationKey(12, 'car-1')
    rememberReservationOrder(12, 'car-1', key, 'order-1')
    api.mine.mockResolvedValue(page([{ ...order, status: 'expired' }]))
    render(Mine)
    await flushPromises()
    expect(reservationKey(12, 'car-1')).not.toBe(key)
  })
})
