import { onUnmounted, ref, watch } from 'vue'
import { carpoolAPI, type ManualOrder } from '@/api/carpool'
import { carpoolErrorKey } from '@/utils/carpool'
import { manualCommandMemory as fallback, type ManualCommand, type PendingCommand } from '@/utils/carpoolManualJournal'

export function useCarpoolManualCommand(actor: () => number | undefined, scope: string) {
  const pending = ref<PendingCommand | null>(null)
  const busy = ref(false)
  const error = ref('')
  let alive = true
  const name = (id: number) => `carpool:manual:${id}:${scope}`
  function store(value: PendingCommand, initial = false) {
    const current = fallback.get(name(value.actor))
    if ((!initial || current) && current?.key !== value.key) return false
    fallback.set(name(value.actor), value)
    try { sessionStorage.setItem(name(value.actor), JSON.stringify(value)) } catch { /* In-memory recovery remains available. */ }
    return true
  }
  function remove(id: number, key: string) {
    if (fallback.get(name(id))?.key !== key) return
    fallback.delete(name(id))
    try { sessionStorage.removeItem(name(id)) } catch { /* Memory entry was removed. */ }
  }
  watch(actor, id => {
    pending.value = null; error.value = ''
    if (!id) return
    let value = fallback.get(name(id))
    try {
      const raw = sessionStorage.getItem(name(id))
      if (raw) value = JSON.parse(raw) as PendingCommand
    } catch { /* Ignore invalid/unavailable storage. */ }
    if (value?.actor === id && typeof value.key === 'string' && typeof value.target === 'string' && value.body && ['enroll', 'withdraw', 'refund'].includes(value.action)) {
      pending.value = { ...value, uncertain: true }
      fallback.set(name(id), pending.value)
    }
  }, { immediate: true, flush: 'sync' })

  async function submit(command?: ManualCommand): Promise<ManualOrder | null> {
    const owner = actor()
    if (!alive || !owner || busy.value) return null
    if (!pending.value) {
      if (!command) return null
      pending.value = { ...JSON.parse(JSON.stringify(command)), actor: owner, key: crypto.randomUUID(), uncertain: false } as PendingCommand
    }
    const attempt = pending.value
    if (attempt.actor !== owner) return null
    busy.value = true; error.value = ''
    // Persist before sending: a reload can happen after the server commits.
    if (!store(attempt, true)) { busy.value = false; error.value = 'carpool.manual.requestConflict'; return null }
    try {
      const result = attempt.action === 'enroll'
        ? await carpoolAPI.enrollManual(attempt.target, attempt.body, attempt.key)
        : attempt.action === 'withdraw'
          ? await carpoolAPI.withdrawManual(attempt.target, attempt.body, attempt.key)
          : await carpoolAPI.refundManual(attempt.target, attempt.body, attempt.key)
      remove(owner, attempt.key)
      if (!alive || actor() !== owner) return null
      pending.value = null
      return result
    } catch (err) {
      const status = (err as { status?: number })?.status ?? 0
      if (attempt.uncertain || ![400, 401, 403, 404, 409, 413, 423, 429].includes(status)) {
        attempt.uncertain = true; store(attempt)
      } else {
        remove(owner, attempt.key)
        if (alive && actor() === owner) pending.value = null
      }
      if (alive && actor() === owner) error.value = carpoolErrorKey(err)
      return null
    } finally { if (alive) busy.value = false }
  }
  function reconcile(confirmed: boolean) {
    if (!confirmed || busy.value || !pending.value || pending.value.actor !== actor()) return
    remove(pending.value.actor, pending.value.key); pending.value = null; error.value = ''
  }
  onUnmounted(() => { alive = false })
  return { pending, busy, error, submit, reconcile }
}
