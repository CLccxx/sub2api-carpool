import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { useCarpoolManualCommand } from '../useCarpoolManualCommand'
import { clearManualCommandJournals, manualCommandMemory } from '@/utils/carpoolManualJournal'

const api = vi.hoisted(() => ({ enrollManual: vi.fn(), withdrawManual: vi.fn(), refundManual: vi.fn() }))
vi.mock('@/api/carpool', () => ({ carpoolAPI: api }))
const actor = ref(701)
let command: ReturnType<typeof useCarpoolManualCommand>
const views: ReturnType<typeof mount>[] = []
function setup() { views.push(mount(defineComponent({ setup() { command = useCarpoolManualCommand(() => actor.value, 'test'); return () => null } }))) }
const input = { action: 'enroll' as const, target: 'car', body: { user_id: 2, amount_minor: 9900, channel: 'alipay' as const, received_at: '2026-09-18T00:00:00Z', reference: 'reference', note: 'note', confirm_received: true } }
beforeEach(() => { vi.resetAllMocks(); sessionStorage.clear(); actor.value++; api.enrollManual.mockResolvedValue({ id: 'order' }) })
afterEach(() => { for (const view of views) view.unmount(); views.length = 0 })

it('freezes a lost response request, restores it after remount and retries the same key/body', async () => {
  setup(); api.enrollManual.mockRejectedValueOnce({ status: 0 })
  await command.submit(input)
  const first = api.enrollManual.mock.calls[0]
  expect(command.pending.value?.uncertain).toBe(true)
  views[0].unmount(); setup()
  expect(command.pending.value?.body).toEqual(input.body)
  await command.submit({ ...input, body: { ...input.body, user_id: 99 } })
  expect(api.enrollManual.mock.calls[1]).toEqual(first)
  expect(command.pending.value).toBeNull()
})
it('does not restore another administrator request and does not retain a definite rejection', async () => {
  setup(); api.enrollManual.mockRejectedValueOnce({ status: 500 }); await command.submit(input)
  actor.value++; await Promise.resolve()
  expect(command.pending.value).toBeNull()
  api.enrollManual.mockRejectedValueOnce({ status: 409 }); await command.submit(input)
  expect(command.pending.value).toBeNull()
})
it('prevents double submission and retains uncertainty even after a subsequent 409', async () => {
  setup()
  let fail!: (value: unknown) => void
  api.enrollManual.mockReturnValueOnce(new Promise((_resolve, reject) => { fail = reject }))
  const first = command.submit(input)
  await command.submit(input)
  expect(api.enrollManual).toHaveBeenCalledTimes(1)
  fail({ status: 0 }); await first
  api.enrollManual.mockRejectedValueOnce({ status: 409 }); await command.submit()
  expect(command.pending.value?.uncertain).toBe(true)
})

it('does not let a late old response erase a newer request journal', async () => {
  setup()
  let finishOld!: (value: unknown) => void
  api.enrollManual.mockReturnValueOnce(new Promise(resolve => { finishOld = resolve }))
  const old = command.submit(input)
  views[0].unmount(); setup()
  command.reconcile(true)
  api.enrollManual.mockRejectedValueOnce({ status: 0 })
  await command.submit({ ...input, target: 'new-car' })
  const key = command.pending.value?.key
  finishOld({ id: 'old-order' }); await old
  views[1].unmount(); setup()
  expect(command.pending.value?.target).toBe('new-car')
  expect(command.pending.value?.key).toBe(key)
})

it('does not restore private recovery data when a request fails after logout', async () => {
  setup()
  let fail!: (value: unknown) => void
  api.enrollManual.mockReturnValueOnce(new Promise((_resolve, reject) => { fail = reject }))
  const submitted = command.submit(input)
  const key = `carpool:manual:${actor.value}:test`
  expect(sessionStorage.getItem(key)).not.toBeNull()
  clearManualCommandJournals()
  actor.value++
  fail({ status: 0 }); await submitted
  expect(sessionStorage.getItem(key)).toBeNull()
  expect(manualCommandMemory.has(key)).toBe(false)
  expect(command.pending.value).toBeNull()
})
