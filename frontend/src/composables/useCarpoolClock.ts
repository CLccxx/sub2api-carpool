import { onMounted, onUnmounted, ref } from 'vue'

// Elapsed monotonic time keeps device-clock adjustments from extending a hold.
export function useCarpoolClock() {
  const now = ref(Date.now())
  let sampledTime = now.value
  let sampledAt = performance.now()
  let synchronized = false
  let timer: ReturnType<typeof setInterval> | undefined
  function sync(serverTime: string, requestStartedAt = performance.now()) {
    const time = Date.parse(serverTime)
    if (!Number.isFinite(time)) return
    const receivedAt = performance.now()
    // Use the full request age conservatively; never extend a hold because a
    // delayed response has an older timestamp than a previously accepted one.
    const candidate = time + Math.max(0, receivedAt - requestStartedAt)
    sampledTime = synchronized ? Math.max(candidate, sampledTime + receivedAt - sampledAt) : candidate
    sampledAt = receivedAt
    synchronized = true
    now.value = sampledTime
  }
  onMounted(() => { timer = setInterval(() => { now.value = sampledTime + performance.now() - sampledAt }, 1000) })
  onUnmounted(() => clearInterval(timer))
  return { now, sync }
}
