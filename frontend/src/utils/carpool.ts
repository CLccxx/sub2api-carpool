import type { Car, Order } from '@/api/carpool'

export function priceToMinor(value: string): number | null {
  if (!/^\d+(\.\d{1,2})?$/.test(value.trim())) return null
  const [whole, decimal = ''] = value.trim().split('.')
  const result = Number(whole) * 100 + Number(decimal.padEnd(2, '0'))
  return Number.isSafeInteger(result) && result > 0 && result <= 1_000_000_000_000 ? result : null
}

export function seatPriceFromTotal(total: number | null, capacity: number): number | null {
  if (total === null || !Number.isSafeInteger(total) || ![2, 3, 4].includes(capacity) || total < capacity || total > 1_000_000_000_000) return null
  return Math.floor((total + Math.floor(capacity / 2)) / capacity)
}

export function formatCarpoolPrice(minor: number, locale = 'zh-CN'): string {
  return new Intl.NumberFormat(locale, { style: 'currency', currency: 'CNY' }).format(minor / 100)
}

export function formatCarpoolTime(value: string | null, locale = 'zh-CN'): string {
  if (!value || !Number.isFinite(Date.parse(value))) return '-'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

export function remainingSeconds(deadline: string | null, now: number): number {
  if (!deadline) return 0
  const difference = Date.parse(deadline) - now
  return Number.isFinite(difference) ? Math.max(0, Math.ceil(difference / 1000)) : 0
}

export function canReserve(car: Pick<Car, 'reservable' | 'status' | 'available_count' | 'recruit_until'> & Partial<Pick<Car, 'enrollment_mode'>>, enabled: boolean, now: number): boolean {
  return car.enrollment_mode !== 'manual' && enabled && car.reservable && car.status === 'recruiting' && car.available_count > 0 && remainingSeconds(car.recruit_until, now) > 0
}

export function canEnroll(car: Car, enabled: boolean, now: number): boolean {
  return enabled && car.enrollment_mode === 'manual' && car.status === 'recruiting' && car.available_count > 0 && remainingSeconds(car.recruit_until, now) > 0
}

export function localCarpoolDate(value: string | number): string {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 19) : ''
}

export function manualDate(value: string, now: number, earliest = 0): string | null {
  const time = new Date(value).getTime()
  return value && Number.isFinite(time) && time >= earliest && time <= now && localCarpoolDate(value).startsWith(value) ? new Date(time).toISOString() : null
}

export const manualRefundPolicy = '未开通服务前退出、招募失败或车辆取消，全额退款，由管理员在站外办理并登记'

export function liveOrder(order: Order, now: number): boolean {
  return (order.status === 'pending' && remainingSeconds(order.expires_at, now) > 0) ||
    (order.status === 'confirmed' && order.membership?.status === 'waiting_deployment')
}

interface ReservationAttempt { key: string; orderId?: string }
const memoryKeys = new Map<string, ReservationAttempt>()
const storageKey = (user: number, car: string) => `carpool:reservation:${user}:${car}`
function readAttempt(name: string): ReservationAttempt | undefined {
  try {
    const raw = sessionStorage.getItem(name)
    if (raw) {
      const parsed = JSON.parse(raw) as ReservationAttempt
      if (typeof parsed.key === 'string') return parsed
    }
  } catch { /* Storage can be disabled or cleared by the browser. */ }
  return memoryKeys.get(name)
}
function storeAttempt(name: string, attempt: ReservationAttempt): void {
  memoryKeys.set(name, attempt)
  try { sessionStorage.setItem(name, JSON.stringify(attempt)) } catch { /* Keep the in-memory retry attempt. */ }
}
export function reservationKey(user: number, car: string): string {
  const name = storageKey(user, car)
  const existing = readAttempt(name)
  if (existing) return existing.key
  const attempt = { key: crypto.randomUUID() }
  storeAttempt(name, attempt)
  return attempt.key
}
export function rememberReservationOrder(user: number, car: string, key: string, orderId: string): void {
  const name = storageKey(user, car)
  if (readAttempt(name)?.key === key) storeAttempt(name, { key, orderId })
}
export function retireReservationOrder(user: number, car: string, orderId: string): void {
  if (readAttempt(storageKey(user, car))?.orderId === orderId) clearReservationKey(user, car)
}
export function clearReservationKey(user: number, car: string): void {
  const name = storageKey(user, car)
  memoryKeys.delete(name)
  try { sessionStorage.removeItem(name) } catch { /* Memory fallback already cleared. */ }
}

export function carpoolErrorKey(error: unknown): string {
  const status = (error as { status?: number })?.status
  const key = ({ 400: 'invalid', 401: 'unauthorized', 403: 'forbidden', 404: 'unavailable', 409: 'conflict', 423: 'compliance', 429: 'rateLimited' } as Record<number, string>)[status ?? 0]
  return `carpool.errors.${key || 'network'}`
}
