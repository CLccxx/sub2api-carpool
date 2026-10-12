import { beforeEach, describe, expect, it, vi } from 'vitest'
import { beginNodeKeyOperation, readNodeKeyOperation, finishNodeKeyOperation, clearNodeKeyJournals } from '../carpoolKeyJournal'

beforeEach(() => { vi.restoreAllMocks(); clearNodeKeyJournals(); sessionStorage.clear() })
describe('node key operation metadata', () => {
  it('keeps one stable operation per actor and car without storing any returned key', () => {
    const first = beginNodeKeyOperation(1, 'car-1', 'Laptop')
    expect(first.operation).toMatch(/^[a-f0-9-]{36}$/)
    expect(beginNodeKeyOperation(1, 'car-1', 'Different')).toEqual(first)
    expect(readNodeKeyOperation(2, 'car-1')).toBeNull()
    expect(readNodeKeyOperation(1, 'car-2')).toBeNull()
    expect(JSON.parse(sessionStorage.getItem('carpool:node-key:1:car-1')!)).toEqual({ actor: 1, car: 'car-1', name: 'Laptop', operation: first.operation })
  })
  it('normalizes persisted metadata and drops unexpected secret fields on recovery', () => {
    sessionStorage.setItem('carpool:node-key:1:car-1', JSON.stringify({ actor: 1, car: 'car-1', name: 'Laptop', operation: '11111111-1111-4111-8111-111111111111', key: 'must-not-retain' }))
    expect(readNodeKeyOperation(1, 'car-1')).toEqual({ actor: 1, car: 'car-1', name: 'Laptop', operation: '11111111-1111-4111-8111-111111111111' })
    expect(sessionStorage.getItem('carpool:node-key:1:car-1')).not.toContain('must-not-retain')
  })
  it('rejects corrupt and cross-actor metadata', () => {
    sessionStorage.setItem('carpool:node-key:1:car-1', JSON.stringify({ actor: 2, car: 'car-1', name: 'x', operation: 'wrong' }))
    expect(readNodeKeyOperation(1, 'car-1')).toBeNull()
  })
  it('cannot clear a newer operation with a stale completion', () => {
    const first = beginNodeKeyOperation(1, 'car-1', 'Laptop')
    finishNodeKeyOperation(first)
    const next = beginNodeKeyOperation(1, 'car-1', 'Phone')
    finishNodeKeyOperation(first)
    expect(readNodeKeyOperation(1, 'car-1')).toEqual(next)
  })
  it('clears unmounted journals on logout without touching unrelated state, including memory fallback', () => {
    sessionStorage.setItem('unrelated', 'keep')
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('private storage') })
    const first = beginNodeKeyOperation(1, 'car-1', 'Laptop')
    expect(readNodeKeyOperation(1, 'car-1')).toEqual(first)
    clearNodeKeyJournals()
    expect(readNodeKeyOperation(1, 'car-1')).toBeNull()
    expect(sessionStorage.getItem('unrelated')).toBe('keep')
  })
})
