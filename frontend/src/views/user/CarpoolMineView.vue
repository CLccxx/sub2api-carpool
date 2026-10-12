<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { carpoolAPI, type Order } from '@/api/carpool'
import { carpoolErrorKey, clearReservationKey, formatCarpoolPrice, formatCarpoolTime, remainingSeconds, retireReservationOrder } from '@/utils/carpool'
import { useCarpoolClock } from '@/composables/useCarpoolClock'
import { useCarpoolRefresh } from '@/composables/useCarpoolRefresh'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import CarpoolStatus from '@/components/carpool/CarpoolStatus.vue'
import CarpoolTerms from '@/components/carpool/CarpoolTerms.vue'
import CarpoolPayment from '@/components/carpool/CarpoolPayment.vue'
import CarpoolNodeConsole from '@/components/carpool/CarpoolNodeConsole.vue'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'

const { t, locale } = useI18n()
const auth = useAuthStore()
const app = useAppStore()
const controlEnabled = computed(() => resolveFeatureFlag(app.cachedPublicSettings, FeatureFlags.carpoolControl))
const consoleOrder = ref<Order | null>(null)
const { now, sync } = useCarpoolClock()
const orders = ref<Order[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const actionError = ref('')
const target = ref<Order | null>(null)
const detail = ref<Order | null>(null)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const filter = ref('')
let request = 0
let alive = true
const manualMode = computed(() => app.cachedPublicSettings?.carpool_manual_enrollment_enabled === true || orders.value.some(order => order.mode === 'manual'))
const canCancel = (order: Order) => order.mode === 'test' && order.status === 'pending' && remainingSeconds(order.expires_at, now.value) > 0 && app.cachedPublicSettings?.carpool_manual_enrollment_enabled !== true
const targetCancellable = computed(() => target.value && canCancel(target.value) && !loading.value && !busy.value && !error.value)
function countdown(order: Order) { const s = remainingSeconds(order.expires_at, now.value); return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}` }
async function load() {
  if (!alive || busy.value) return
  const ticket = ++request
  const user = auth.user?.id
  const startedAt = performance.now()
  loading.value = true
  error.value = ''
  try {
    const result = await carpoolAPI.mine({ page: page.value, page_size: pageSize.value, status: filter.value || undefined })
    if (!alive || request !== ticket || user !== auth.user?.id) return
    sync(result.server_time, startedAt)
    orders.value = result.items
    if (detail.value) detail.value = result.items.find(o => o.id === detail.value?.id) ?? detail.value
    if (user) for (const order of result.items) {
      if (order.status === 'expired' || order.status === 'cancelled') retireReservationOrder(user, order.car_id, order.id)
    }
    total.value = result.total
    if (target.value) target.value = result.items.find(o => o.id === target.value?.id) ?? null
  } catch (err) {
    if (alive && ticket === request) { error.value = t(carpoolErrorKey(err)); orders.value = [] }
  } finally { if (alive && ticket === request) loading.value = false }
}
function openCancel(order: Order) { if (!busy.value && !loading.value) { target.value = order; actionError.value = '' } }
async function cancelOrder() {
  if (!targetCancellable.value || !target.value) return
  const order = target.value
  const user = auth.user?.id
  busy.value = true
  actionError.value = ''
  try {
    await carpoolAPI.cancelOrder(order.id)
    if (!alive || user !== auth.user?.id) return
    if (user) clearReservationKey(user, order.car_id)
    target.value = null
    app.showSuccess(t('carpool.cancelledSuccess'))
  } catch (err) { if (alive && user === auth.user?.id) actionError.value = t(carpoolErrorKey(err)) }
  finally { busy.value = false; if (alive) await load() }
}
function changePage(value: number) { page.value = value; void load() }
function changeSize(value: number) { pageSize.value = Math.min(value, 100); changePage(1) }
useCarpoolRefresh(load, () => { alive = false; request++ })
</script>

<template>
  <AppLayout>
    <section class="min-w-0 space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3"><div class="flex flex-wrap items-center gap-3"><h1 class="text-xl font-semibold">{{ t('carpool.mine') }}</h1><span class="rounded bg-amber-50 px-2 py-1 text-xs font-medium text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ t(manualMode ? 'carpool.manual.mode' : 'carpool.testOrders') }}</span></div><RouterLink to="/carpool" class="btn btn-secondary">{{ t('carpool.lobby') }}<Icon name="arrowRight" size="sm" /></RouterLink></header>
      <div class="flex flex-wrap items-center justify-between gap-3 border-y border-gray-200 py-3 dark:border-dark-700"><select v-model="filter" :aria-label="t('carpool.all')" class="input w-auto max-w-full" @change="changePage(1)"><option value="">{{ t('carpool.all') }}</option><option v-for="status in (manualMode ? ['confirmed', 'cancelled'] : ['pending', 'confirmed', 'expired', 'cancelled'])" :key="status" :value="status">{{ t(manualMode && status === 'confirmed' ? 'carpool.manual.confirmed' : `carpool.status.${status}`) }}</option></select><button class="btn btn-secondary" :aria-label="t('carpool.refresh')" :title="t('carpool.refresh')" :disabled="loading || busy" @click="load"><Icon name="refresh" size="md" :class="{ 'animate-spin': loading }" /></button></div>
      <div v-if="error" role="alert" class="flex flex-wrap items-center gap-3 text-sm text-red-700 dark:text-red-300">{{ error }}<button class="btn btn-secondary" @click="load">{{ t('carpool.retry') }}</button></div>
      <p v-if="actionError && !target" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ actionError }}</p>
      <p v-if="loading && !orders.length" role="status" class="py-12 text-center text-gray-500">{{ t('carpool.loading') }}</p>
      <div v-else-if="!orders.length && !error" class="flex flex-col items-center gap-3 py-16 text-gray-500"><Icon name="inbox" size="xl" /><p>{{ t('carpool.noOrders') }}</p></div>
      <div class="divide-y divide-gray-200 dark:divide-dark-700" :aria-busy="loading">
        <article v-for="order in orders" :key="order.id" class="min-w-0 py-5" :data-testid="`order-${order.id}`">
          <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0"><h2 class="break-words text-lg font-semibold [overflow-wrap:anywhere]">{{ order.snapshot.name }}</h2><p class="mt-1 text-sm text-gray-500">{{ t('carpool.seat', { n: order.seat_no }) }} · {{ formatCarpoolPrice(order.snapshot.price_minor, locale) }} / {{ t('carpool.days', { n: order.snapshot.service_days }) }}</p></div><CarpoolStatus :manual="order.mode === 'manual'" :status="order.status === 'pending' && remainingSeconds(order.expires_at, now) === 0 ? 'expired' : order.status" /></div>
          <CarpoolPayment v-if="order.mode === 'manual' && order.payment" class="mt-3" :payment="order.payment" />
          <p class="mt-3 break-all font-mono text-xs text-gray-500">{{ t('carpool.orderId') }}: {{ order.id }}</p>
          <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
            <div class="space-y-1 text-sm"><p v-if="canCancel(order)" class="flex items-center gap-2 font-medium text-amber-800 dark:text-amber-300"><Icon name="clock" size="sm" />{{ t('carpool.remaining') }} <span class="tabular-nums">{{ countdown(order) }}</span></p><div v-if="order.membership" class="flex flex-wrap items-center gap-2"><CarpoolStatus :status="order.membership.status" /><span v-if="!order.membership.service_start" class="text-gray-500">{{ t('carpool.serviceNotStarted') }}</span><span v-else class="text-gray-500">{{ formatCarpoolTime(order.membership.service_start, locale) }} / {{ formatCarpoolTime(order.membership.service_end, locale) }}</span></div><p class="text-gray-500">{{ formatCarpoolTime(order.created_at, locale) }}</p></div>
            <div class="flex flex-wrap gap-2"><button v-if="controlEnabled && order.membership" :data-testid="`console-${order.id}`" class="btn btn-secondary" @click="consoleOrder = order"><Icon name="key" size="sm" />{{ t('carpool.control.console') }}</button><button class="btn btn-secondary" @click="detail = order">{{ t('carpool.details') }}</button><button v-if="canCancel(order)" class="btn btn-secondary" :disabled="busy || loading || !!error" :data-testid="`cancel-${order.id}`" @click="openCancel(order)"><Icon name="x" size="sm" />{{ t('carpool.cancelOrder') }}</button></div>
          </div>
        </article>
      </div>
      <Pagination v-if="total > 0" :page="page" :page-size="pageSize" :total="total" @update:page="changePage" @update:page-size="changeSize" />
    </section>
    <CarpoolNodeConsole :show="!!consoleOrder" :car-i-d="consoleOrder?.car_id || ''" :car-name="consoleOrder?.snapshot.name || ''" :enabled="controlEnabled" @close="consoleOrder = null" />
    <BaseDialog :show="!!detail" :title="detail?.snapshot.name || t('carpool.details')" @close="detail = null"><div v-if="detail" class="space-y-4"><CarpoolTerms :terms="detail.snapshot" /><CarpoolPayment v-if="detail.mode === 'manual' && detail.payment" :payment="detail.payment" /></div></BaseDialog>
    <BaseDialog :show="!!target" :title="t('carpool.cancelOrder')" width="narrow" :show-close-button="!busy" :close-on-escape="!busy" @close="!busy && (target = null)"><p class="text-sm">{{ t('carpool.cancelOrderNotice') }}</p><p v-if="actionError" role="alert" class="mt-3 text-sm text-red-700 dark:text-red-300">{{ actionError }}</p><template #footer><div class="flex flex-wrap justify-end gap-3"><button class="btn btn-secondary" :disabled="busy" @click="target = null">{{ t('carpool.close') }}</button><button class="btn btn-danger" data-testid="confirm-cancel-order" :disabled="!targetCancellable" @click="cancelOrder">{{ busy ? t('carpool.processing') : t('carpool.cancelOrder') }}</button></div></template></BaseDialog>
  </AppLayout>
</template>
