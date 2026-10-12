<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { carpoolAPI, type Car, type Order } from '@/api/carpool'
import { canEnroll, canReserve, carpoolErrorKey, clearReservationKey, formatCarpoolPrice, formatCarpoolTime, liveOrder, reservationKey, rememberReservationOrder } from '@/utils/carpool'
import { useCarpoolClock } from '@/composables/useCarpoolClock'
import { useCarpoolRefresh } from '@/composables/useCarpoolRefresh'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import CarpoolStatus from '@/components/carpool/CarpoolStatus.vue'
import CarpoolTerms from '@/components/carpool/CarpoolTerms.vue'

const { t, locale } = useI18n()
const router = useRouter()
const app = useAppStore()
const auth = useAuthStore()
const { now, sync } = useCarpoolClock()
const cars = ref<Car[]>([])
const orders = ref<Order[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const filter = ref('')
const loading = ref(false)
const error = ref('')
const actionError = ref('')
const busy = ref(false)
const target = ref<Car | null>(null)
const detail = ref<Car | null>(null)
const contact = ref<Car | null>(null)
let request = 0
let alive = true
const enabled = computed(() => app.cachedPublicSettings?.carpool_main_enabled === true)
const manualEnabled = computed(() => enabled.value && app.cachedPublicSettings?.carpool_manual_enrollment_enabled === true && app.cachedPublicSettings?.carpool_test_orders_enabled !== true)
const manualMode = computed(() => app.cachedPublicSettings?.carpool_manual_enrollment_enabled === true || cars.value.some(car => car.enrollment_mode === 'manual'))
const contactInfo = computed(() => app.cachedPublicSettings?.contact_info?.trim() || '')
const testsEnabled = computed(() => enabled.value && app.cachedPublicSettings?.carpool_test_orders_enabled === true && app.cachedPublicSettings?.carpool_manual_enrollment_enabled !== true)
const participation = computed(() => new Map(orders.value.filter(o => liveOrder(o, now.value)).map(o => [o.car_id, o])))

async function liveOrders(status: string, ticket: number): Promise<Order[]> {
  const result: Order[] = []
  for (let p = 1; ; p++) {
    if (!alive || ticket !== request) return []
    const response = await carpoolAPI.mine({ page: p, page_size: 100, status })
    result.push(...response.items)
    if (p * response.page_size >= response.total || response.items.length === 0) return result
  }
}
async function load() {
  if (busy.value || !alive) return
  const ticket = ++request
  const user = auth.user?.id
  const startedAt = performance.now()
  loading.value = true
  error.value = ''
  try {
    const [catalog, pending, confirmed] = await Promise.all([
      carpoolAPI.listCars({ page: page.value, page_size: pageSize.value, status: filter.value || undefined }),
      liveOrders('pending', ticket), liveOrders('confirmed', ticket)
    ])
    if (!alive || ticket !== request || auth.user?.id !== user) return
    sync(catalog.server_time, startedAt)
    cars.value = catalog.items
    orders.value = [...pending, ...confirmed]
    total.value = catalog.total
    if (target.value) target.value = catalog.items.find(c => c.id === target.value?.id) ?? null
    if (contact.value) contact.value = catalog.items.find(c => c.id === contact.value?.id && canEnroll(c, manualEnabled.value, now.value)) ?? null
  } catch (err) {
    if (alive && ticket === request) { error.value = t(carpoolErrorKey(err)); cars.value = []; orders.value = [] }
  } finally { if (alive && ticket === request) loading.value = false }
}
function available(car: Car) {
  return !loading.value && !error.value && !busy.value && !participation.value.has(car.id) && canReserve(car, testsEnabled.value, now.value)
}
function recruitmentClosed(car: Car) {
  return car.status === 'needs_review' || car.status === 'cancelled' || car.status === 'closed_unfilled' ||
    (car.status === 'recruiting' && Date.parse(car.recruit_until) <= now.value)
}
// Aggregate occupancy only; the public catalog does not expose seat assignments.
function seatState(car: Car, position: number) {
  if (position <= car.confirmed_count) return 'confirmed'
  if (position <= car.confirmed_count + car.held_count) return 'held'
  return 'available'
}
function openReservation(car: Car) {
  if (!available(car)) return
  target.value = car
  actionError.value = ''
}
async function reserve() {
  const car = target.value
  const user = auth.user?.id
  if (!car || !user || !available(car)) return
  busy.value = true
  actionError.value = ''
  try {
    const key = reservationKey(user, car.id)
    const order = await carpoolAPI.reserve(car.id, key)
    if (!alive || auth.user?.id !== user) return
    rememberReservationOrder(user, car.id, key, order.id)
    if (order.status !== 'pending' && order.status !== 'confirmed') {
      clearReservationKey(user, car.id)
      actionError.value = t('carpool.errors.conflict')
    } else {
      target.value = null
      app.showSuccess(t('carpool.reservationSuccess'))
      void router.push('/carpool/mine')
    }
  } catch (err) {
    if (alive && auth.user?.id === user) actionError.value = t(carpoolErrorKey(err))
  } finally {
    busy.value = false
    if (alive) await load()
  }
}
function changePage(value: number) { page.value = value; void load() }
function changeSize(value: number) { pageSize.value = Math.min(value, 100); changePage(1) }
useCarpoolRefresh(load, () => { alive = false; request++ })
</script>

<template>
  <AppLayout>
    <section class="carpool-page space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap items-center gap-3"><h1 class="text-xl font-semibold">{{ t('carpool.lobby') }}</h1><span class="rounded bg-amber-50 px-2 py-1 text-xs font-medium text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ t(manualMode ? 'carpool.manual.mode' : 'carpool.testOrders') }}</span></div>
        <RouterLink to="/carpool/mine" class="btn btn-secondary">{{ t('carpool.mine') }}<Icon name="arrowRight" size="sm" /></RouterLink>
      </header>
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t(manualMode ? 'carpool.manual.notice' : 'carpool.lobbyUI.notice') }}</p>
      <div class="flex flex-wrap items-center justify-between gap-3 border-y border-gray-200 py-3 dark:border-dark-700">
        <select v-model="filter" :aria-label="t('carpool.all')" class="input w-auto max-w-full" @change="changePage(1)">
          <option value="">{{ t('carpool.all') }}</option>
          <option v-for="status in ['recruiting', 'awaiting_deployment', 'needs_review', 'closed_unfilled', 'cancelled']" :key="status" :value="status">{{ t(status === 'needs_review' ? 'carpool.manual.reviewCar' : `carpool.status.${status}`) }}</option>
        </select>
        <div class="flex items-center gap-3"><span v-if="!testsEnabled && !manualEnabled" class="text-sm text-gray-500">{{ t(manualMode ? 'carpool.manual.disabled' : 'carpool.readOnly') }}</span><button class="btn btn-secondary" :aria-label="t('carpool.refresh')" :title="t('carpool.refresh')" :disabled="loading || busy" @click="load"><Icon name="refresh" size="md" :class="{ 'animate-spin': loading }" /></button></div>
      </div>
      <div v-if="error" role="alert" class="flex flex-wrap items-center gap-3 text-sm text-red-700 dark:text-red-300">{{ error }}<button class="btn btn-secondary" @click="load">{{ t('carpool.retry') }}</button></div>
      <p v-if="actionError && !target" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ actionError }}</p>
      <p v-if="loading && !cars.length" role="status" class="py-12 text-center text-gray-500">{{ t('carpool.loading') }}</p>
      <div v-else-if="!cars.length && !error" class="flex flex-col items-center gap-3 py-16 text-gray-500"><Icon name="users" size="xl" /><p>{{ t('carpool.noCars') }}</p></div>
      <div class="carpool-catalog" :aria-busy="loading">
        <article v-for="car in cars" :key="car.id" class="carpool-card border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800" :data-testid="`car-${car.id}`">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <h2 class="min-w-0 flex-1 break-words text-base font-semibold [overflow-wrap:anywhere]">{{ car.name }}</h2>
            <CarpoolStatus :status="car.status" />
          </div>
          <div class="carpool-highlights">
            <div>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('carpool.lobbyUI.joined') }}</p>
              <p data-testid="seat-availability" class="carpool-availability" :class="[car.available_count > 0 && !recruitmentClosed(car) ? 'text-primary-700 dark:text-primary-300' : 'text-gray-500 dark:text-gray-400', { 'carpool-availability-closed': recruitmentClosed(car) }]">
                <span v-if="recruitmentClosed(car)" class="text-base font-medium">{{ t('carpool.lobbyUI.closed') }}</span>
                <template v-else>{{ car.confirmed_count }}<span class="ml-1 text-sm font-normal text-gray-500 dark:text-gray-400">/ {{ car.capacity }}</span></template>
              </p>
            </div>
            <div class="min-w-0 text-right" data-testid="seat-price">
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('carpool.lobbyUI.perSeat') }}</p>
              <p class="carpool-price">{{ formatCarpoolPrice(car.price_minor, locale) }}</p>
              <p class="text-xs text-gray-500 dark:text-gray-400">/ {{ t('carpool.days', { n: car.service_days }) }}</p>
            </div>
          </div>
          <div class="carpool-occupancy">
            <div data-testid="seat-summary" class="flex gap-1.5" role="img" :aria-label="t('carpool.lobbyUI.seatSummary', { confirmed: car.confirmed_count, held: car.held_count, available: car.available_count, capacity: car.capacity })">
              <span v-for="position in car.capacity" :key="position" class="carpool-seat" :data-seat-state="seatState(car, position)" aria-hidden="true"><Icon :name="seatState(car, position) === 'confirmed' ? 'check' : seatState(car, position) === 'held' ? 'clock' : 'user'" size="sm" /></span>
            </div>
            <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
              <span v-if="!recruitmentClosed(car)" data-testid="seats-remaining">{{ t('carpool.lobbyUI.seatsLeft') }} <strong class="font-medium text-gray-700 dark:text-gray-200">{{ car.available_count }}</strong></span>
              <span v-if="car.enrollment_mode !== 'manual'">{{ t('carpool.held') }} <strong class="font-medium text-amber-700 dark:text-amber-300">{{ car.held_count }}</strong></span>
            </div>
          </div>
          <dl class="carpool-timing border-t border-gray-100 dark:border-dark-700">
            <div><dt>{{ t('carpool.deadline') }}</dt><dd>{{ formatCarpoolTime(car.recruit_until, locale) }}</dd></div>
            <div><dt>{{ t('carpool.deliveryHours') }}</dt><dd>{{ t('carpool.hoursAfterFull', { n: car.delivery_hours }) }}</dd></div>
            <div v-if="car.delivery_due"><dt>{{ t('carpool.deliveryDue') }}</dt><dd>{{ formatCarpoolTime(car.delivery_due, locale) }}</dd></div>
          </dl>
          <div class="carpool-actions">
            <button class="btn btn-secondary" @click="detail = car">{{ t('carpool.details') }}</button>
            <RouterLink v-if="participation.has(car.id)" to="/carpool/mine" class="btn btn-secondary">{{ t('carpool.myOrder') }}</RouterLink>
            <template v-else-if="car.enrollment_mode === 'manual'">
              <button v-if="contactInfo && canEnroll(car, manualEnabled, now)" class="btn btn-primary" :data-testid="`contact-${car.id}`" :disabled="loading || !!error" @click="contact = car"><Icon name="user" size="sm" />{{ t('carpool.manual.contact') }}</button>
              <span v-else-if="!contactInfo && canEnroll(car, manualEnabled, now)" class="text-xs text-gray-500">{{ t('carpool.manual.contactMissing') }}</span>
            </template>
            <button v-else class="btn btn-primary" :data-testid="`reserve-${car.id}`" :disabled="!available(car)" @click="openReservation(car)">{{ t('carpool.reserve') }}</button>
          </div>
        </article>
      </div>
      <Pagination v-if="total > 0" :page="page" :page-size="pageSize" :total="total" @update:page="changePage" @update:page-size="changeSize" />
    </section>
    <BaseDialog :show="!!contact" :title="t('carpool.manual.contact')" @close="contact = null"><p class="mb-3 break-words font-medium">{{ contact?.name }}</p><p class="whitespace-pre-wrap break-words text-sm [overflow-wrap:anywhere]" data-testid="carpool-contact-info">{{ contactInfo }}</p></BaseDialog>
    <BaseDialog :show="!!detail" :title="detail?.name || t('carpool.details')" @close="detail = null">
      <div v-if="detail" class="space-y-4">
        <CarpoolTerms :terms="detail" />
        <dl class="text-sm"><dt class="text-gray-500">{{ t('carpool.created') }}</dt><dd class="mt-1">{{ formatCarpoolTime(detail.created_at, locale) }}</dd></dl>
      </div>
    </BaseDialog>
    <BaseDialog :show="!!target" :title="t('carpool.reserveConfirm')" :show-close-button="!busy" :close-on-escape="!busy" @close="!busy && (target = null)">
      <div v-if="target" class="space-y-5"><p class="text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.reserveNotice') }}</p><h2 class="break-words font-semibold">{{ target.name }}</h2><CarpoolTerms :terms="target" /><p v-if="actionError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ actionError }}</p></div>
      <template #footer><div class="flex flex-wrap justify-end gap-3"><button class="btn btn-secondary" :disabled="busy" @click="target = null">{{ t('carpool.cancel') }}</button><button class="btn btn-primary" data-testid="confirm-reserve" :disabled="!target || !available(target)" @click="reserve">{{ busy ? t('carpool.processing') : t('carpool.reserve') }}</button></div></template>
    </BaseDialog>
  </AppLayout>
</template>

<style scoped>
.carpool-page { min-width: 0; letter-spacing: 0; container-type: inline-size; }
.carpool-page .btn { white-space: normal; }
.carpool-catalog { display: grid; min-width: 0; grid-template-columns: minmax(0, 1fr); gap: 16px; }
.carpool-card { display: flex; flex-direction: column; min-width: 0; padding: 16px; border-radius: 8px; }
.carpool-highlights { display: grid; grid-template-columns: minmax(0, 0.8fr) minmax(0, 1.2fr); align-items: start; gap: 12px; margin-top: 16px; }
.carpool-availability { margin-top: 4px; font-size: 32px; line-height: 1.25; font-weight: 600; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.carpool-availability-closed { margin-top: 8px; font-size: 16px; line-height: 1.5; }
.carpool-price { margin: 8px 0 2px; font-size: 24px; line-height: 1.25; font-weight: 600; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.carpool-occupancy { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 10px; margin: 12px 0; }
.carpool-seat { display: flex; flex-shrink: 0; align-items: center; justify-content: center; width: 28px; height: 30px; border-radius: 5px; }
.carpool-seat[data-seat-state='confirmed'] { @apply border border-primary-200 bg-primary-50 text-primary-700 dark:border-primary-800 dark:bg-primary-900/30 dark:text-primary-300; }
.carpool-seat[data-seat-state='held'] { @apply border border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-300; }
.carpool-seat[data-seat-state='available'] { @apply border border-dashed border-gray-300 text-gray-400 dark:border-dark-500 dark:text-gray-400; }
.carpool-timing { display: grid; gap: 8px; padding-top: 12px; font-size: 12px; }
.carpool-timing > div { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px; }
.carpool-timing dt { @apply text-gray-500 dark:text-gray-400; }
.carpool-timing dd { margin-left: auto; min-width: 0; text-align: right; overflow-wrap: anywhere; }
.carpool-actions { display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: stretch; gap: 8px; margin-top: auto; padding-top: 14px; }
.carpool-actions .btn { min-width: 0; min-height: 40px; justify-content: center; overflow-wrap: anywhere; }
@container (min-width: 660px) { .carpool-catalog { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@container (min-width: 1020px) { .carpool-catalog { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
</style>
