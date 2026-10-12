import { onMounted, onUnmounted } from 'vue'

export function useCarpoolRefresh(refresh: () => Promise<void>, invalidate: () => void) {
  let timer: ReturnType<typeof setInterval> | undefined
  const visibleRefresh = () => { if (!document.hidden) void refresh() }
  onMounted(() => {
    void refresh()
    timer = setInterval(visibleRefresh, 15000)
    window.addEventListener('focus', visibleRefresh)
    document.addEventListener('visibilitychange', visibleRefresh)
  })
  onUnmounted(() => {
    invalidate()
    clearInterval(timer)
    window.removeEventListener('focus', visibleRefresh)
    document.removeEventListener('visibilitychange', visibleRefresh)
  })
}
