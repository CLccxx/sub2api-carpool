import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { Car, Membership, Page } from '@/api/carpool'
import { formatCarpoolTime } from '@/utils/carpool'
import CarpoolView from '../CarpoolView.vue'

const api = vi.hoisted(() => ({
  listCars: vi.fn(), getCar: vi.fn(), createCar: vi.fn(), updateCar: vi.fn(),
  publishCar: vi.fn(), cancelCar: vi.fn(), members: vi.fn(), enrollManual: vi.fn(), manualOrders: vi.fn(), manualOrder: vi.fn(), withdrawManual: vi.fn(), refundManual: vi.fn(),
  controlInfo: vi.fn(), nodeConnection: vi.fn()
}))
const app = vi.hoisted(() => ({ cachedPublicSettings: { carpool_main_enabled: true, carpool_control_enabled: false, carpool_manual_enrollment_enabled: false, carpool_test_orders_enabled: true, contact_info: '' } }))
const users = vi.hoisted(() => ({ list: vi.fn(), getById: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => app }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/api/admin/users', () => ({ default: users }))
vi.mock('@/api/carpool', () => ({ carpoolAPI: api }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') })
}))

const car = (overrides: Partial<Car> = {}): Car => ({
  id: 'car-1', enrollment_mode: 'test', name: 'Test car', capacity: 3, price_minor: 1001, currency: 'CNY',
  service_days: 30, recruit_until: '2027-01-01T12:00:00.000Z', delivery_hours: 48,
  refund_policy: 'Cancel before deployment', status: 'draft', version: 1,
  created_at: '2026-09-16T00:00:00Z', published_at: null, full_at: null, delivery_due: null,
  confirmed_count: 0, held_count: 0, available_count: 3, reservable: false,
  server_time: '2026-09-16T00:00:00Z', ...overrides
})
const page = <T,>(items: T[], overrides: Partial<Page<T>> = {}): Page<T> => ({
  items, total: items.length, page: 1, page_size: 20, server_time: car().server_time, ...overrides
})
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const TableStub = defineComponent({
  props: ['data', 'columns', 'loading'],
  template: `<div><div v-for="row in data" :key="row.id"><div v-for="column in columns" :key="column.key"><slot :name="'cell-'+column.key" :row="row" :value="row[column.key]">{{ row[column.key] }}</slot></div></div><slot v-if="!data.length && !loading" name="empty" /></div>`
})
const DialogStub = defineComponent({
  props: ['show', 'title', 'closeOnEscape', 'showCloseButton', 'closeOnClickOutside'], emits: ['close'],
  template: `<section v-if="show" role="dialog"><h2>{{title}}</h2><slot/><slot name="footer"/><button data-testid="dialog-dismiss" @click="$emit('close')">Dismiss</button></section>`
})
const PaginationStub = defineComponent({
  props: ['total', 'page', 'pageSize'], emits: ['update:page', 'update:pageSize'],
  template: `<nav><button data-testid="next-page" @click="$emit('update:page', page+1)">Next</button><button data-testid="page-size" @click="$emit('update:pageSize', 50)">50</button></nav>`
})
let wrapper: VueWrapper
const mountView = () => {
  wrapper = mount(CarpoolView, {
    global: {
      stubs: { AppLayout: { template: '<main><slot/></main>' }, DataTable: TableStub, BaseDialog: DialogStub, Pagination: PaginationStub, Icon: true }
    }
  })
  return wrapper
}
const field = (name: string) => wrapper.get(`[data-testid="carpool-${name}"]`)
async function fillForm() {
  await field('name').setValue('New car')
  await field('capacity').setValue('4')
  await field('price').setValue('19.99')
  await field('service-days').setValue('60')
  await field('deadline').setValue('2027-02-01T12:30')
  await field('delivery-hours').setValue('72')
  await field('refund-policy').setValue('Refund before deployment')
}
async function openAction(action: string, id = 'car-1') {
  await wrapper.get(`[data-test="${action}-${id}"]`).trigger('click')
  await flushPromises()
}

describe('admin carpool vehicles', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    sessionStorage.clear()
    app.cachedPublicSettings.carpool_manual_enrollment_enabled = false
    app.cachedPublicSettings.carpool_test_orders_enabled = true
    app.cachedPublicSettings.carpool_control_enabled = false
    api.listCars.mockResolvedValue(page([car()], { total: 45 }))
    api.getCar.mockResolvedValue(car())
    api.createCar.mockResolvedValue(car())
    api.updateCar.mockResolvedValue(car())
    api.publishCar.mockResolvedValue(car({ status: 'recruiting' }))
    api.cancelCar.mockResolvedValue(car({ status: 'cancelled' }))
    api.members.mockResolvedValue(page([]))
  })
  afterEach(() => wrapper?.unmount())

  it('opens node settings for the selected car only when control is enabled', async () => {
    mountView(); await flushPromises()
    expect(wrapper.find('[data-test="node-car-1"]').exists()).toBe(false)
    wrapper.unmount(); app.cachedPublicSettings.carpool_control_enabled = true
    api.controlInfo.mockResolvedValue({ main_instance_id: 'main-1' }); api.nodeConnection.mockRejectedValue({ status: 404 })
    mountView(); await flushPromises(); await openAction('node')
    expect(api.nodeConnection).toHaveBeenCalledWith('car-1')
    expect(wrapper.find('[data-testid="node-register"]').exists()).toBe(true)
    expect(field('status-filter').findAll('option')).toHaveLength(10)
  })

  it('searches an account, reviews receipt details and retries a lost enrollment without changing target or key', async () => {
    app.cachedPublicSettings.carpool_manual_enrollment_enabled = true
    app.cachedPublicSettings.carpool_test_orders_enabled = false
    const manual = car({ enrollment_mode: 'manual', status: 'recruiting' })
    api.listCars.mockResolvedValue(page([manual])); api.getCar.mockResolvedValue(manual)
    users.list.mockResolvedValue({ items: [{ id: 2, email: 'member@example.test', status: 'active' }], total: 1 })
    api.enrollManual.mockRejectedValueOnce({ status: 0 }).mockResolvedValueOnce({ id: 'manual-order' })
    mountView(); await flushPromises(); await openAction('enroll')
    await wrapper.get('[data-testid="manual-user-search"]').setValue('member')
    await wrapper.get('[data-testid="manual-user-search-submit"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="manual-user-2"]').setValue(true)
    await wrapper.get('[data-testid="manual-received-at"]').setValue('2026-09-15T00:00:00')
    await wrapper.get('[data-testid="manual-received-confirm"]').setValue(true)
    await wrapper.get('[data-testid="manual-review"]').trigger('click'); await flushPromises()
    expect(api.enrollManual).not.toHaveBeenCalled()
    expect(wrapper.get('[role="dialog"]').text()).toContain('member@example.test')
    await wrapper.get('[data-testid="manual-enroll-confirm"]').trigger('click'); await flushPromises()
    const original = api.enrollManual.mock.calls[0]
    expect(original[1].user_id).toBe(2)
    expect(original[1].amount_minor).toBe(1001)
    expect(wrapper.find('[data-testid="manual-user-search"]').exists()).toBe(false)
    await wrapper.get('[data-testid="manual-retry"]').trigger('click'); await flushPromises()
    expect(api.enrollManual.mock.calls[1]).toEqual(original)
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })

  it('invalidates selected users immediately and ignores search results arriving out of order', async () => {
    app.cachedPublicSettings.carpool_manual_enrollment_enabled = true
    app.cachedPublicSettings.carpool_test_orders_enabled = false
    const manual = car({ enrollment_mode: 'manual', status: 'recruiting' })
    api.listCars.mockResolvedValue(page([manual])); api.getCar.mockResolvedValue(manual)
    const old = deferred<{ items: { id: number; email: string; status: string }[]; total: number }>()
    users.list.mockReturnValueOnce(old.promise).mockResolvedValueOnce({ items: [{ id: 3, email: 'new@example.test', status: 'active' }], total: 1 })
    mountView(); await flushPromises(); await openAction('enroll')
    await wrapper.get('[data-testid="manual-user-search"]').setValue('old')
    await wrapper.get('[data-testid="manual-user-search-submit"]').trigger('click')
    await wrapper.get('[data-testid="manual-user-search"]').setValue('new')
    await wrapper.get('[data-testid="manual-user-search-submit"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="manual-user-3"]').setValue(true)
    old.resolve({ items: [{ id: 2, email: 'old@example.test', status: 'active' }], total: 1 }); await flushPromises()
    expect(wrapper.find('[data-testid="manual-user-2"]').exists()).toBe(false)
    await wrapper.get('[data-testid="manual-user-search"]').setValue('different')
    await wrapper.get('[data-testid="manual-review"]').trigger('click')
    expect(wrapper.find('[data-testid="manual-enroll-confirm"]').exists()).toBe(false)
    expect(api.enrollManual).not.toHaveBeenCalled()
  })

  it('keeps cancelled manual orders and pending refunds actionable when enrollment is disabled', async () => {
    const order = { id: 'manual-order', car_id: 'car-1', user_id: 2, user_email: 'member@example.test', seat_no: 1, mode: 'manual', status: 'cancelled', snapshot: car(), expires_at: null, membership: null,
      receipt: { recorded_by: 1, reference: 'receipt', note: 'internal' }, refund_record: { reason: 'closed' },
      payment: { amount_minor: 1001, channel: 'wechat', received_at: '2026-09-14T00:00:00Z', recorded_at: '2026-09-14T01:00:00Z', refund: { status: 'pending', amount_minor: 1001, refunded_at: null } } }
    api.manualOrders.mockResolvedValue(page([order])); api.manualOrder.mockResolvedValue(order)
    api.refundManual.mockResolvedValue({ ...order, payment: { ...order.payment, refund: { ...order.payment.refund, status: 'refunded' } } })
    mountView(); await flushPromises()
    await wrapper.get('[data-testid="manual-orders-tab"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('member@example.test')
    await wrapper.get('[data-testid="refund-manual-order"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="manual-refund-at"]').setValue('2026-09-15T00:00:00')
    await wrapper.get('[data-testid="manual-order-confirm"]').trigger('click'); await flushPromises()
    expect(api.refundManual).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="manual-order-ack"]').setValue(true)
    await wrapper.get('[data-testid="manual-order-confirm"]').trigger('click'); await flushPromises()
    expect(api.refundManual.mock.calls[0][1].amount_minor).toBe(1001)
  })

  it('uses the admin list, paginates and resets pagination on filters and page size changes', async () => {
    mountView(); await flushPromises()
    expect(api.listCars).toHaveBeenLastCalledWith({ page: 1, page_size: 20 }, true)
    await wrapper.get('[data-testid="next-page"]').trigger('click'); await flushPromises()
    expect(api.listCars).toHaveBeenLastCalledWith({ page: 2, page_size: 20 }, true)
    await field('status-filter').setValue('cancelled'); await flushPromises()
    expect(api.listCars).toHaveBeenLastCalledWith({ page: 1, page_size: 20, status: 'cancelled' }, true)
    await wrapper.get('[data-testid="page-size"]').trigger('click'); await flushPromises()
    expect(api.listCars).toHaveBeenLastCalledWith({ page: 1, page_size: 50, status: 'cancelled' }, true)
    expect(field('status-filter').findAll('option')).toHaveLength(7)
  })

  it('defaults to a 1400 CNY total and submits total and rounded per-seat prices', async () => {
    mountView(); await flushPromises(); await field('create').trigger('click')
    expect((field('price').element as HTMLInputElement).value).toBe('1400.00')
    await fillForm(); await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.createCar).toHaveBeenCalledWith({ name: 'New car', capacity: 4, total_price_minor: 1999, price_minor: 500, currency: 'CNY', service_days: 60, recruit_until: new Date('2027-02-01T12:30').toISOString(), delivery_hours: 72, refund_policy: 'Refund before deployment' })
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })
  it.each([[2, 70000], [3, 46667], [4, 35000]])('divides the configured total across %i seats', async (capacity, seatPrice) => {
    mountView(); await flushPromises(); await field('create').trigger('click'); await fillForm()
    await field('price').setValue('1400.00'); await field('capacity').setValue(String(capacity))
    expect(wrapper.get('[data-testid="carpool-seat-price-preview"]').text()).toContain((seatPrice / 100).toFixed(2))
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.createCar).toHaveBeenCalledWith(expect.objectContaining({ total_price_minor: 140000, price_minor: seatPrice }))
  })
  it('keeps the save action outside the scrolling form and linked to its submission', async () => {
    mountView(); await flushPromises(); await field('create').trigger('click')
    expect(field('save').attributes('form')).toBe('carpool-car-form')
    expect(wrapper.get('form').attributes('id')).toBe('carpool-car-form')
    expect(wrapper.get('form').find('[data-testid="carpool-save"]').exists()).toBe(false)
  })

  it.each([
    ['name', '车'.repeat(54)], ['name', ' '], ['price', '1.999'], ['price', '0'], ['price', '0.01'], ['price', '0.03'], ['price', '1e2'],
    ['service-days', '0'], ['service-days', '3651'], ['service-days', '1.5'],
    ['deadline', '2020-01-01T00:00'], ['deadline', '2040-01-01T00:00'],
    ['delivery-hours', '0'], ['delivery-hours', '8761'],
    ['refund-policy', ' '], ['refund-policy', '车'.repeat(1334)]
  ])('rejects invalid %s input', async (name, value) => {
    mountView(); await flushPromises(); await field('create').trigger('click'); await fillForm()
    await field(name).setValue(value); await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.createCar).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toBeTruthy()
  })

  it('edits fresh draft details and prevents duplicate submits or dismissal while saving', async () => {
    const save = deferred<Car>(); api.updateCar.mockReturnValue(save.promise)
    mountView(); await flushPromises(); await openAction('edit')
    expect(api.getCar).toHaveBeenCalledWith('car-1', true)
    expect((field('price').element as HTMLInputElement).value).toBe('10.01')
    await field('name').setValue('Updated'); await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit'); await wrapper.get('[data-testid="dialog-dismiss"]').trigger('click')
    expect(api.updateCar).toHaveBeenCalledTimes(1)
    expect(api.updateCar.mock.calls[0][1].price_minor).toBe(1001)
    expect(api.updateCar.mock.calls[0][1]).not.toHaveProperty('total_price_minor')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    const dialog = wrapper.findAllComponents(DialogStub).find(dialog => dialog.props('show'))!
    expect(dialog.props('closeOnEscape')).toBe(false)
    expect(dialog.props('showCloseButton')).toBe(false)
    save.resolve(car()); await flushPromises()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })

  it('edits a total-priced draft without treating its per-seat quote as the total', async () => {
    api.getCar.mockResolvedValue(car({ total_price_minor: 140000, price_minor: 46667 }))
    mountView(); await flushPromises(); await openAction('edit')
    expect((field('price').element as HTMLInputElement).value).toBe('1400.00')
    await field('capacity').setValue('2')
    expect(wrapper.get('[data-testid="carpool-seat-price-preview"]').text()).toContain('700.00')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.updateCar).toHaveBeenCalledWith('car-1', expect.objectContaining({ total_price_minor: 140000, price_minor: 70000, capacity: 2 }))
  })

  it('shows frozen terms before publishing and requires explicit confirmation', async () => {
    mountView(); await flushPromises(); await openAction('publish')
    expect(wrapper.get('[role="dialog"]').text()).toContain('Cancel before deployment')
    expect(api.publishCar).not.toHaveBeenCalled()
    await field('publish-confirm').trigger('click'); await flushPromises()
    expect(api.publishCar).toHaveBeenCalledWith('car-1')
  })

  it('requires a byte-limited cancellation reason and acknowledgement of confirmed members', async () => {
    api.getCar.mockResolvedValue(car({ status: 'awaiting_deployment', confirmed_count: 2 }))
    mountView(); await flushPromises(); await openAction('cancel')
    await field('cancel-confirm').trigger('click')
    expect(api.cancelCar).not.toHaveBeenCalled()
    await field('cancel-reason').setValue('车'.repeat(667))
    await field('confirm-members').setValue(true); await field('cancel-confirm').trigger('click')
    expect(api.cancelCar).not.toHaveBeenCalled()
    await field('cancel-reason').setValue('Unable to deliver')
    await field('confirm-members').setValue(false); await field('cancel-confirm').trigger('click')
    expect(api.cancelCar).not.toHaveBeenCalled()
    await field('confirm-members').setValue(true); await field('cancel-confirm').trigger('click'); await flushPromises()
    expect(api.cancelCar).toHaveBeenCalledWith('car-1', 'Unable to deliver', true)
  })

  it('refreshes status after 409 and disables an action no longer permitted', async () => {
    api.publishCar.mockRejectedValue({ status: 409 })
    mountView(); await flushPromises(); await openAction('publish')
    api.getCar.mockResolvedValue(car({ status: 'recruiting' }))
    api.listCars.mockResolvedValue(page([car({ status: 'recruiting' })]))
    await field('publish-confirm').trigger('click'); await flushPromises()
    expect(api.getCar).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('carpool.adminUI.conflict')
    expect(wrapper.find('[data-test="edit-car-1"]').exists()).toBe(false)
    expect(field('publish-confirm').attributes('disabled')).toBeDefined()
  })

  it('offers retry after a failed list request and ignores stale list responses', async () => {
    api.listCars.mockRejectedValueOnce({ status: 500 })
    mountView(); await flushPromises()
    await field('list-retry').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Test car')
    const old = deferred<Page<Car>>()
    api.listCars.mockReturnValueOnce(old.promise).mockResolvedValueOnce(page([car({ id: 'new', name: 'Newest', status: 'cancelled' })]))
    await field('status-filter').setValue('draft')
    await field('status-filter').setValue('cancelled'); await flushPromises()
    old.resolve(page([car({ name: 'Stale' })])); await flushPromises()
    expect(wrapper.text()).toContain('Newest')
    expect(wrapper.text()).not.toContain('Stale')
  })

  it('ignores detail responses arriving after a dialog is dismissed', async () => {
    const old = deferred<Car>(); api.getCar.mockReturnValue(old.promise)
    mountView(); await flushPromises(); await openAction('edit')
    await wrapper.get('[data-testid="dialog-dismiss"]').trigger('click')
    await field('create').trigger('click'); old.resolve(car()); await flushPromises()
    expect((field('name').element as HTMLInputElement).value).toBe('')
  })

  it('shows members and their status, order, seat and unstarted service with separate pagination', async () => {
    const member: Membership = { id: 'member-1', car_id: 'car-1', user_id: 99, seat_no: 2, order_id: 'order-1', status: 'cancelled', created_at: car().created_at, service_start: null, service_end: null }
    api.members.mockResolvedValue(page([member], { total: 30 }))
    mountView(); await flushPromises(); await openAction('members')
    expect(api.members).toHaveBeenCalledWith('car-1', { page: 1, page_size: 20 })
    expect(wrapper.get('[role="dialog"]').text()).toContain('order-1')
    expect(wrapper.get('[role="dialog"]').text()).toContain('99')
    expect(wrapper.get('[role="dialog"]').text()).toContain('carpool.serviceNotStarted')
    await wrapper.get('[role="dialog"] [data-testid="next-page"]').trigger('click'); await flushPromises()
    expect(api.members).toHaveBeenLastCalledWith('car-1', { page: 2, page_size: 20 })
  })

  it('only shows draft edit/publish and only cancellable status actions', async () => {
    api.listCars.mockResolvedValue(page(['draft', 'recruiting', 'awaiting_deployment', 'closed_unfilled', 'cancelled'].map(status => car({ id: status, status: status as Car['status'] }))))
    mountView(); await flushPromises()
    expect(wrapper.findAll('[data-test^="edit-"]')).toHaveLength(1)
    expect(wrapper.findAll('[data-test^="publish-"]')).toHaveLength(1)
    expect(wrapper.findAll('[data-test^="cancel-"]')).toHaveLength(3)
    expect(wrapper.findAll('[data-test^="members-"]')).toHaveLength(5)
  })

  it('reconciles an uncertain create instead of resubmitting a possibly committed draft', async () => {
    api.createCar.mockRejectedValueOnce({ status: 500 }).mockResolvedValueOnce(car())
    mountView(); await flushPromises(); await field('create').trigger('click'); await fillForm()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('carpool.adminUI.createUncertain')
    expect((field('price').element as HTMLInputElement).value).toBe('19.99')
    expect(field('save').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.createCar).toHaveBeenCalledTimes(1)
    expect(api.listCars).toHaveBeenCalledTimes(2)
    expect(api.listCars).toHaveBeenLastCalledWith({ page: 1, page_size: 20, status: 'draft' }, true)
    await field('review-create').trigger('click'); await flushPromises()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })
  it('labels member orders as test-only', async () => {
    mountView(); await flushPromises(); await openAction('members')
    expect(wrapper.get('[role="dialog"]').text()).toContain('carpool.testOrders')
  })
  it('does not unlock creation when a recovery list request is superseded', async () => {
    api.createCar.mockRejectedValueOnce({ status: 0 })
    mountView(); await flushPromises(); await field('create').trigger('click'); await fillForm()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const old = deferred<ReturnType<typeof page>>()
    const newer = deferred<ReturnType<typeof page>>()
    api.listCars.mockReturnValueOnce(old.promise).mockReturnValueOnce(newer.promise)
    await field('review-create').trigger('click')
    await field('status-filter').setValue('recruiting')
    old.resolve(page([car()])); await flushPromises()
    newer.reject({ status: 503 }); await flushPromises()
    expect(field('save').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    expect(api.createCar).toHaveBeenCalledTimes(1)
  })

  it('does not request an empty car id when creation returns a conflict', async () => {
    api.createCar.mockRejectedValueOnce({ status: 409 })
    mountView(); await flushPromises(); await field('create').trigger('click'); await fillForm()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.getCar).not.toHaveBeenCalled()
    expect(api.listCars).toHaveBeenCalledTimes(2)
    expect(field('save').attributes('disabled')).toBeUndefined()
  })

  it('requires a new acknowledgement when confirmed membership changes on cancellation conflict', async () => {
    api.getCar.mockResolvedValueOnce(car({ confirmed_count: 1 })).mockResolvedValueOnce(car({ confirmed_count: 2 }))
    api.cancelCar.mockRejectedValueOnce({ status: 409 })
    mountView(); await flushPromises(); await openAction('cancel')
    await field('cancel-reason').setValue('Unable to deliver'); await field('confirm-members').setValue(true)
    await field('cancel-confirm').trigger('click'); await flushPromises()
    expect((field('confirm-members').element as HTMLInputElement).checked).toBe(false)
    await field('cancel-confirm').trigger('click'); await flushPromises()
    expect(api.cancelCar).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('carpool.adminUI.validation.confirmMembers')
    await field('confirm-members').setValue(true); await field('cancel-confirm').trigger('click'); await flushPromises()
    expect(api.cancelCar).toHaveBeenCalledTimes(2)
  })

  it('blocks further mutations until a failed conflict refresh is retried', async () => {
    api.publishCar.mockRejectedValueOnce({ status: 409 })
    api.getCar.mockResolvedValueOnce(car()).mockRejectedValueOnce({ status: 500 }).mockResolvedValueOnce(car({ status: 'recruiting' }))
    mountView(); await flushPromises(); await openAction('publish')
    await field('publish-confirm').trigger('click'); await flushPromises()
    expect(field('publish-confirm').attributes('disabled')).toBeDefined()
    await field('detail-retry').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('carpool.adminUI.notDraft')
    expect(api.getCar).toHaveBeenCalledTimes(3)
    expect(api.publishCar).toHaveBeenCalledTimes(1)
  })

  it.each(['publish', 'cancel'])('prevents double %s submissions and busy dialog dismissal', async action => {
    const pending = deferred<Car>()
    const operation = action === 'publish' ? api.publishCar : api.cancelCar
    operation.mockReturnValue(pending.promise)
    mountView(); await flushPromises(); await openAction(action)
    if (action === 'cancel') await field('cancel-reason').setValue('No capacity')
    await field(`${action}-confirm`).trigger('click')
    await field(`${action}-confirm`).trigger('click')
    await wrapper.get('[data-testid="dialog-dismiss"]').trigger('click')
    expect(operation).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    expect(wrapper.findAllComponents(DialogStub).find(dialog => dialog.props('show'))!.props('closeOnEscape')).toBe(false)
    pending.resolve(car()); await flushPromises()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })

  it('never enables stale draft actions when fresh details show a published car', async () => {
    api.getCar.mockResolvedValue(car({ status: 'recruiting' }))
    mountView(); await flushPromises(); await openAction('edit')
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.text()).toContain('carpool.adminUI.notDraft')
    expect(api.updateCar).not.toHaveBeenCalled()
  })

  it('offers a retry on member errors and ignores older member-page responses', async () => {
    api.members.mockRejectedValueOnce({ status: 503 }).mockResolvedValueOnce(page([], { total: 60 }))
    mountView(); await flushPromises(); await openAction('members')
    await field('members-retry').trigger('click'); await flushPromises()
    const old = deferred<Page<Membership>>()
    api.members.mockReturnValueOnce(old.promise).mockResolvedValueOnce(page([], { total: 60 }))
    await wrapper.get('[role="dialog"] [data-testid="next-page"]').trigger('click')
    await wrapper.get('[role="dialog"] [data-testid="next-page"]').trigger('click'); await flushPromises()
    expect(api.members).toHaveBeenLastCalledWith('car-1', { page: 3, page_size: 20 })
    old.resolve(page([{ id: 'stale-member', car_id: 'car-1', user_id: 99, seat_no: 1, order_id: 'stale-order', status: 'cancelled', created_at: car().created_at, service_start: null, service_end: null }])); await flushPromises()
    expect(wrapper.text()).not.toContain('stale-member')
    expect(wrapper.get('[role="dialog"]').text()).toContain('carpool.noMembers')
  })

  it('accepts byte and numeric upper bounds without floating-point price rounding', async () => {
    mountView(); await flushPromises(); await field('create').trigger('click'); await fillForm()
    await field('name').setValue('车'.repeat(53) + 'A')
    await field('price').setValue('10000000000.00')
    await field('service-days').setValue('3650'); await field('delivery-hours').setValue('8760')
    await field('refund-policy').setValue('车'.repeat(1333) + 'A')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.createCar).toHaveBeenCalledWith(expect.objectContaining({ total_price_minor: 1_000_000_000_000, price_minor: 250_000_000_000, service_days: 3650, delivery_hours: 8760 }))
  })

  it('lists confirmed, held and available seats together with created, recruitment and delivery times', async () => {
    const vehicle = car({ confirmed_count: 1, held_count: 1, available_count: 1, delivery_due: '2027-01-03T12:00:00Z' })
    api.listCars.mockResolvedValue(page([vehicle]))
    mountView(); await flushPromises()
    const table = wrapper.findComponent(TableStub)
    expect(table.props('columns').map((column: { key: string }) => column.key)).toEqual(expect.arrayContaining(['seats', 'times']))
    for (const label of ['carpool.confirmed', 'carpool.held', 'carpool.available']) expect(wrapper.text()).toContain(label)
    for (const time of [vehicle.created_at, vehicle.recruit_until, vehicle.delivery_due]) {
      expect(table.text()).toContain(formatCarpoolTime(time, 'en'))
    }
    for (const label of ['created', 'deadline', 'deliveryDue']) expect(table.text()).toContain(`carpool.${label}`)
  })

  it('caps vehicle and member page sizes at 100 even when global pagination settings offer larger values', async () => {
    mountView(); await flushPromises()
    wrapper.findComponent(PaginationStub).vm.$emit('update:pageSize', 250)
    await flushPromises()
    expect(api.listCars).toHaveBeenLastCalledWith({ page: 1, page_size: 100 }, true)
    expect(wrapper.findComponent(PaginationStub).props('pageSize')).toBe(100)
    await openAction('members')
    wrapper.findAllComponents(PaginationStub)[1].vm.$emit('update:pageSize', 250)
    await flushPromises()
    expect(api.members).toHaveBeenLastCalledWith('car-1', { page: 1, page_size: 100 })
    expect(wrapper.findAllComponents(PaginationStub)[1].props('pageSize')).toBe(100)
  })
})
