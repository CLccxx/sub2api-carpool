import type { EnrollmentInput, RefundInput, WithdrawalInput } from '@/api/carpool'

export type ManualCommand = { action: 'enroll'; target: string; body: EnrollmentInput } | { action: 'withdraw'; target: string; body: WithdrawalInput } | { action: 'refund'; target: string; body: RefundInput }
export type PendingCommand = ManualCommand & { actor: number; key: string; uncertain: boolean }
export const manualCommandMemory = new Map<string, PendingCommand>()

// Auth cleanup must work even when the carpool page has already unmounted.
export function clearManualCommandJournals(): void {
  manualCommandMemory.clear()
  try {
    for (let index = sessionStorage.length - 1; index >= 0; index--) {
      const key = sessionStorage.key(index)
      if (key?.startsWith('carpool:manual:')) sessionStorage.removeItem(key)
    }
  } catch { /* Private storage may be disabled. */ }
}
