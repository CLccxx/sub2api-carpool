import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { afterEach, expect, it, vi } from 'vitest'
import { useCarpoolClock } from '../useCarpoolClock'

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })
it('accounts for a delayed response and never rewinds synchronized time', () => {
  vi.useFakeTimers()
  let elapsed = 0
  vi.spyOn(performance, 'now').mockImplementation(() => elapsed)
  let clock!: ReturnType<typeof useCarpoolClock>
  const wrapper = mount(defineComponent({ setup() { clock = useCarpoolClock(); return () => null } }))
  try {
    const sample = Date.parse('2026-09-16T12:00:00Z')
    clock.sync('2026-09-16T12:00:00Z', 0)
    elapsed = 10000
    vi.advanceTimersByTime(1000)
    expect(clock.now.value).toBe(sample + 10000)
    clock.sync('2026-09-16T12:00:00Z', 0)
    expect(clock.now.value).toBe(sample + 10000)
    clock.sync('2026-09-16T11:59:55Z', 10000)
    expect(clock.now.value).toBe(sample + 10000)
  } finally { wrapper.unmount() }
})
