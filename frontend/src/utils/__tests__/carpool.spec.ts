import { beforeEach, describe, expect, it } from 'vitest'
import type { Car } from '@/api/carpool'
import { priceToMinor, seatPriceFromTotal, reservationKey, clearReservationKey, rememberReservationOrder, retireReservationOrder, remainingSeconds, canReserve, canEnroll, localCarpoolDate, manualDate } from '../carpool'

describe('carpool boundary helpers', () => {
  beforeEach(() => sessionStorage.clear())
  it('parses money exactly and rejects excess decimals, exponents and invalid bounds', () => {
    expect(priceToMinor('0.29')).toBe(29)
    expect(priceToMinor('99.90')).toBe(9990)
    for (const v of ['0', '-1', '1.001', '1e2', '', '10000000000.01']) expect(priceToMinor(v)).toBeNull()
  })
  it('splits integer minor units and rejects totals that cannot quote every seat', () => {
    expect(seatPriceFromTotal(140000, 2)).toBe(70000)
    expect(seatPriceFromTotal(140000, 3)).toBe(46667)
    expect(seatPriceFromTotal(140000, 4)).toBe(35000)
    expect(seatPriceFromTotal(1999, 4)).toBe(500)
    for (const value of [null, 0, -1, 3, 140000.5, NaN, Infinity, 1_000_000_000_001]) expect(seatPriceFromTotal(value, 4)).toBeNull()
    for (const capacity of [0, 1, 2.5, 5, NaN]) expect(seatPriceFromTotal(140000, capacity)).toBeNull()
  })
  it('keeps retry keys per user and car until a definite terminal result', () => {
    const first = reservationKey(2, 'car')
    expect(reservationKey(2, 'car')).toBe(first)
    expect(reservationKey(3, 'car')).not.toBe(first)
    expect(reservationKey(2, 'other')).not.toBe(first)
    clearReservationKey(2, 'car')
    expect(reservationKey(2, 'car')).not.toBe(first)
  })
  it('uses synchronized time and clamps countdown at zero', () => {
    expect(remainingSeconds(null, 0)).toBe(0)
    expect(remainingSeconds('2026-09-16T12:00:10Z', Date.parse('2026-09-16T12:00:00Z'))).toBe(10)
    expect(remainingSeconds('2026-09-16T12:00:10Z', Date.parse('2026-09-16T12:00:11Z'))).toBe(0)
    expect(remainingSeconds('invalid', 0)).toBe(0)
  })
  it('retires only the matching terminal attempt, not a newer retry key', () => {
    const key = reservationKey(77, 'car')
    rememberReservationOrder(77, 'car', key, 'first-order')
    retireReservationOrder(77, 'car', 'older-order')
    expect(reservationKey(77, 'car')).toBe(key)
    retireReservationOrder(77, 'car', 'first-order')
    const next = reservationKey(77, 'car')
    expect(next).not.toBe(key)
    retireReservationOrder(77, 'car', 'first-order')
    expect(reservationKey(77, 'car')).toBe(next)
  })
  it('requires server permission, capacity, enabled tests and future deadline', () => {
    const car = { reservable: true, status: 'recruiting', available_count: 1, recruit_until: '2026-09-16T12:01:00Z' }
    const now = Date.parse('2026-09-16T12:00:00Z')
    expect(canReserve(car, true, now)).toBe(true)
    expect(canReserve(car, false, now)).toBe(false)
    expect(canReserve({ ...car, available_count: 0 }, true, now)).toBe(false)
    expect(canReserve(car, true, now + 60000)).toBe(false)
    expect(canReserve({ ...car, enrollment_mode: 'manual' }, true, now)).toBe(false)
  })
  it('allows manual enrollment only for a recruiting manual car before its deadline', () => {
    const now = Date.parse('2026-09-16T12:00:00Z')
    const car = { enrollment_mode: 'manual', status: 'recruiting', available_count: 1, recruit_until: '2026-09-16T12:01:00Z' } as Car
    expect(canEnroll(car, true, now)).toBe(true)
    expect(canEnroll(car, false, now)).toBe(false)
    expect(canEnroll({ ...car, enrollment_mode: 'test' }, true, now)).toBe(false)
    expect(canEnroll({ ...car, available_count: 0 }, true, now)).toBe(false)
    expect(canEnroll(car, true, now + 60000)).toBe(false)
    for (const status of ['draft', 'needs_review', 'awaiting_deployment', 'cancelled', 'closed_unfilled'] as const) {
      expect(canEnroll({ ...car, status }, true, now)).toBe(false)
    }
  })
  it('validates local receipt/refund timestamps against synchronized bounds without calendar rollover', () => {
    const now = new Date('2026-09-16T12:00:00').getTime()
    const received = now - 60000
    expect(manualDate(localCarpoolDate(received), now)).toBe(new Date(received).toISOString())
    expect(manualDate(localCarpoolDate(received), now, received)).toBe(new Date(received).toISOString())
    expect(manualDate(localCarpoolDate(received - 1000), now, received)).toBeNull()
    expect(manualDate(localCarpoolDate(now + 1000), now)).toBeNull()
    for (const value of ['', 'invalid', '2026-02-30T10:00:00']) expect(manualDate(value, now)).toBeNull()
  })
})
