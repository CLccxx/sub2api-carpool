<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { carpoolAPI, type Car, type CarInput, type CarStatus, type Membership } from '@/api/carpool'
import { canEnroll, carpoolErrorKey, formatCarpoolPrice, formatCarpoolTime } from '@/utils/carpool'
import { useAppStore } from '@/stores/app'
import { useCarpoolClock } from '@/composables/useCarpoolClock'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import CarpoolCarForm from '@/components/carpool/CarpoolCarForm.vue'
import CarpoolTerms from '@/components/carpool/CarpoolTerms.vue'
import CarpoolStatus from '@/components/carpool/CarpoolStatus.vue'
import CarpoolEnrollment from '@/components/carpool/CarpoolEnrollment.vue'
import CarpoolManualOrders from '@/components/carpool/CarpoolManualOrders.vue'
import CarpoolNodeConnection from '@/components/carpool/CarpoolNodeConnection.vue'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'
import type { Column } from '@/components/common/types'

const { t, locale } = useI18n()
const app = useAppStore()
const controlEnabled = computed(() => resolveFeatureFlag(app.cachedPublicSettings, FeatureFlags.carpoolControl))
const nodeCar = ref<Car>()
const manualEnabled = computed(() => app.cachedPublicSettings?.carpool_manual_enrollment_enabled === true && app.cachedPublicSettings?.carpool_test_orders_enabled !== true)
const view = ref<'cars' | 'orders'>('cars')
const orderCarID = ref('')
const enrollmentCar = ref<Car>()
function inspectOrders(id = '') { orderCarID.value = id; view.value = 'orders' }
function manualChanged() { notice.value = 'carpool.manual.saved'; void loadCars() }
function openMembers(car: Car) { if (car.enrollment_mode === 'manual') inspectOrders(car.id); else openDialog('members', car) }
function writable(car: Car) { return car.enrollment_mode === 'manual' ? manualEnabled.value : app.cachedPublicSettings?.carpool_manual_enrollment_enabled !== true }
const { now, sync } = useCarpoolClock()
const statuses = computed<CarStatus[]>(() => ['draft', 'recruiting', 'awaiting_deployment', 'needs_review', 'closed_unfilled', 'cancelled', ...(controlEnabled.value ? ['active', 'paused', 'completed'] as const : [])])
const cars = ref<Car[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const status = ref('')
const loading = ref(false)
const listError = ref('')
const notice = ref('')
let listRequest = 0
let alive = true
type Mode = 'create' | 'edit' | 'publish' | 'cancel' | 'members' | 'details'
const mode = ref<Mode | null>(null)
const selectedId = ref('')
const selectedCar = ref<Car>()
const dialogLoading = ref(false)
const detailError = ref('')
const actionError = ref('')
const busy = ref(false)
const needsRefresh = ref(false)
const uncertainCreate = ref(false)
const reason = ref('')
const confirmMembers = ref(false)
const cancelValidation = ref('')
let dialogEpoch = 0
let detailRequest = 0
let memberRequest = 0
const members = ref<Membership[]>([])
const memberPage = ref(1)
const memberPageSize = ref(20)
const memberTotal = ref(0)
const memberLoading = ref(false)
const memberError = ref('')

const columns = computed<Column[]>(() => [
  { key: 'name', label: t('carpool.name') }, { key: 'status', label: t('carpool.adminUI.status') },
  { key: 'seats', label: t('carpool.capacity') }, { key: 'price_minor', label: t('carpool.price') },
  { key: 'times', label: t('carpool.adminUI.times') }, { key: 'actions', label: t('carpool.adminUI.actions') }
])
const memberColumns = computed<Column[]>(() => [
  { key: 'id', label: t('carpool.adminUI.memberId') }, { key: 'user_id', label: t('carpool.adminUI.userId') },
  { key: 'seat_no', label: t('carpool.adminUI.seatNumber') }, { key: 'order_id', label: t('carpool.orderId') },
  { key: 'status', label: t('carpool.adminUI.status') }, { key: 'created_at', label: t('carpool.created') },
  { key: 'service', label: t('carpool.adminUI.service') }
])
const dialogTitle = computed(() => t({
  create: 'carpool.adminUI.create', edit: 'carpool.adminUI.edit', publish: 'carpool.adminUI.publish',
  cancel: 'carpool.adminUI.cancelCar', members: 'carpool.adminUI.members', details: 'carpool.details'
}[mode.value || 'details']))
const cancellable = (car: Car) => ['draft', 'recruiting', 'awaiting_deployment', 'needs_review'].includes(car.status)
const actionAllowed = computed(() => {
  if (busy.value || dialogLoading.value || needsRefresh.value) return false
  if (mode.value === 'create') return !uncertainCreate.value
  if (!selectedCar.value) return false
  if (mode.value === 'edit' || mode.value === 'publish') return selectedCar.value.status === 'draft' && writable(selectedCar.value)
  return mode.value === 'cancel' && cancellable(selectedCar.value)
})
function errorKey(error: unknown) {
  const key = carpoolErrorKey(error)
  return key === 'carpool.errors.network' ? 'carpool.adminUI.requestFailed' : key
}
async function loadCars(): Promise<boolean> {
  const startedAt = performance.now()
  const request = ++listRequest
  loading.value = true
  listError.value = ''
  try {
    const result = await carpoolAPI.listCars({ page: page.value, page_size: pageSize.value, ...(status.value ? { status: status.value } : {}) }, true)
    if (!alive || request !== listRequest) return false
    sync(result.server_time, startedAt)
    const lastPage = Math.max(1, Math.ceil(result.total / pageSize.value))
    if (page.value > lastPage) { page.value = lastPage; return await loadCars() }
    cars.value = result.items
    total.value = result.total
    return true
  } catch (error) {
    if (alive && request === listRequest) { listError.value = errorKey(error); cars.value = []; total.value = 0 }
    return false
  } finally {
    if (alive && request === listRequest) loading.value = false
  }
}
function changePage(value: number) { page.value = value; void loadCars() }
const boundedPageSize = (value: number) => Math.min(100, Math.max(1, Number.isFinite(value) ? Math.trunc(value) : 20))
function changePageSize(value: number) { pageSize.value = boundedPageSize(value); page.value = 1; void loadCars() }
function changeStatus() { page.value = 1; void loadCars() }

async function loadDetail() {
  const startedAt = performance.now()
  const epoch = dialogEpoch
  const request = ++detailRequest
  const id = selectedId.value
  dialogLoading.value = true
  detailError.value = ''
  try {
    const car = await carpoolAPI.getCar(id, true)
    if (!alive || epoch !== dialogEpoch || request !== detailRequest) return
    selectedCar.value = car
    sync(car.server_time, startedAt)
    needsRefresh.value = false
    if (mode.value === 'members') await loadMembers()
  } catch (error) {
    if (alive && epoch === dialogEpoch && request === detailRequest) {
      detailError.value = errorKey(error)
      needsRefresh.value = true
    }
  } finally {
    if (alive && epoch === dialogEpoch && request === detailRequest) dialogLoading.value = false
  }
}
function openDialog(next: Mode, car?: Car) {
  if (busy.value) return
  if (next === 'create' && uncertainCreate.value) return
  dialogEpoch++
  mode.value = next
  selectedId.value = car?.id || ''
  selectedCar.value = undefined
  dialogLoading.value = false
  detailError.value = ''; actionError.value = ''; notice.value = ''; cancelValidation.value = ''
  reason.value = ''; confirmMembers.value = false; needsRefresh.value = false
  members.value = []; memberPage.value = 1; memberPageSize.value = 20; memberTotal.value = 0; memberError.value = ''; memberLoading.value = false
  if (car) void loadDetail()
}
function closeDialog() {
  if (busy.value) return
  dialogEpoch++
  mode.value = null
  selectedCar.value = undefined
}
async function loadMembers() {
  const startedAt = performance.now()
  const epoch = dialogEpoch
  const request = ++memberRequest
  memberLoading.value = true
  memberError.value = ''
  try {
    const result = await carpoolAPI.members(selectedId.value, { page: memberPage.value, page_size: memberPageSize.value })
    if (!alive || epoch !== dialogEpoch || request !== memberRequest) return
    sync(result.server_time, startedAt)
    const lastPage = Math.max(1, Math.ceil(result.total / memberPageSize.value))
    if (memberPage.value > lastPage) { memberPage.value = lastPage; await loadMembers(); return }
    members.value = result.items
    memberTotal.value = result.total
  } catch (error) {
    if (alive && epoch === dialogEpoch && request === memberRequest) { memberError.value = errorKey(error); members.value = []; memberTotal.value = 0 }
  } finally {
    if (alive && epoch === dialogEpoch && request === memberRequest) memberLoading.value = false
  }
}
function changeMemberPage(value: number) { memberPage.value = value; void loadMembers() }
function changeMemberPageSize(value: number) { memberPageSize.value = boundedPageSize(value); memberPage.value = 1; void loadMembers() }

async function mutate(operation: () => Promise<Car>, success: string) {
  if (!actionAllowed.value) return
  const epoch = dialogEpoch
  const startedAt = performance.now()
  busy.value = true
  actionError.value = ''
  try {
    const car = await operation()
    if (!alive || epoch !== dialogEpoch) return
    sync(car.server_time, startedAt)
    notice.value = success
    busy.value = false
    closeDialog()
    await loadCars()
  } catch (error) {
    if (!alive || epoch !== dialogEpoch) return
    const responseStatus = (error as { status?: number })?.status ?? 0
    if (mode.value === 'create' && (responseStatus === 0 || responseStatus >= 500)) {
      uncertainCreate.value = true
      actionError.value = 'carpool.adminUI.createUncertain'
      // A just-created draft must be visible even if creation began from the
      // awaiting-deployment filter or a later page.
      page.value = 1
      status.value = 'draft'
      await loadCars()
    } else if (responseStatus === 409) {
      actionError.value = 'carpool.adminUI.conflict'
      confirmMembers.value = false
      needsRefresh.value = !!selectedId.value
      await Promise.all([loadCars(), ...(selectedId.value ? [loadDetail()] : [])])
    } else actionError.value = errorKey(error)
  } finally {
    if (alive && epoch === dialogEpoch) busy.value = false
  }
}
async function reviewCreate() {
  if (busy.value || loading.value) return
  const epoch = dialogEpoch
  busy.value = true
  page.value = 1
  status.value = 'draft'
  try {
    const accepted = await loadCars()
    if (alive && epoch === dialogEpoch && accepted) {
      uncertainCreate.value = false
      busy.value = false
      closeDialog()
    }
  } finally {
    if (alive && epoch === dialogEpoch) busy.value = false
  }
}
function save(input: CarInput) {
  if (mode.value !== 'create' && mode.value !== 'edit') return
  void mutate(() => mode.value === 'create' ? carpoolAPI.createCar(input) : carpoolAPI.updateCar(selectedId.value, input), 'carpool.adminUI.saved')
}
function publish() {
  if (mode.value === 'publish') void mutate(() => carpoolAPI.publishCar(selectedId.value), 'carpool.adminUI.published')
}
function cancelCar() {
  if (mode.value !== 'cancel' || !actionAllowed.value) return
  cancelValidation.value = ''
  const text = reason.value.trim()
  if (!text || new TextEncoder().encode(text).length > 2000) { cancelValidation.value = 'carpool.adminUI.validation.cancelReason'; return }
  if (selectedCar.value!.confirmed_count > 0 && !confirmMembers.value) { cancelValidation.value = 'carpool.adminUI.validation.confirmMembers'; return }
  void mutate(() => carpoolAPI.cancelCar(selectedId.value, text, selectedCar.value!.confirmed_count > 0 && confirmMembers.value), 'carpool.adminUI.cancelled')
}
onMounted(loadCars)
onUnmounted(() => { alive = false; listRequest++; dialogEpoch++; memberRequest++; detailRequest++ })
</script>

<template>
  <AppLayout>
    <div class="carpool-admin min-w-0 space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap items-center gap-3"><h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('carpool.admin') }}</h1><span class="rounded bg-amber-50 px-2 py-1 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ t(manualEnabled ? 'carpool.manual.mode' : 'carpool.testOrders') }}</span></div>
        <button data-testid="carpool-create" class="btn btn-primary" :disabled="busy || uncertainCreate" @click="openDialog('create')">
          <Icon name="plus" size="sm" class="mr-2" />{{ t('carpool.adminUI.create') }}
        </button>
      </header>
      <div role="tablist" class="flex gap-4 border-b border-gray-200 dark:border-dark-700"><button role="tab" :aria-selected="view === 'cars'" class="border-b-2 px-1 py-2 text-sm font-medium" :class="view === 'cars' ? 'border-primary-500 text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500'" @click="view = 'cars'">{{ t('carpool.manual.vehicles') }}</button><button role="tab" :aria-selected="view === 'orders'" data-testid="manual-orders-tab" class="border-b-2 px-1 py-2 text-sm font-medium" :class="view === 'orders' ? 'border-primary-500 text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500'" @click="inspectOrders()">{{ t('carpool.manual.orders') }}</button></div>
      <p v-if="manualEnabled && !app.cachedPublicSettings?.contact_info?.trim()" role="status" class="text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.manual.contactSetup') }}</p>
      <CarpoolEnrollment :show="!!enrollmentCar" :initial-car="enrollmentCar" :enabled="manualEnabled" @close="enrollmentCar = undefined" @changed="manualChanged" @inspect="inspectOrders" />
      <CarpoolNodeConnection :show="!!nodeCar" :car-i-d="nodeCar?.id || ''" :car-name="nodeCar?.name || ''" :enabled="controlEnabled" @close="nodeCar = undefined" />
      <CarpoolManualOrders v-if="view === 'orders'" :car-i-d="orderCarID" @changed="manualChanged" />
      <div v-show="view === 'cars'" class="space-y-5">
      <div class="flex flex-wrap items-end gap-3">
        <div class="min-w-0 max-w-full">
          <label for="carpool-status-filter" class="input-label">{{ t('carpool.adminUI.status') }}</label>
          <select id="carpool-status-filter" v-model="status" data-testid="carpool-status-filter" class="input" @change="changeStatus">
            <option value="">{{ t('carpool.all') }}</option>
            <option v-for="item in statuses" :key="item" :value="item">{{ t(item === 'needs_review' ? 'carpool.manual.reviewCar' : `carpool.status.${item}`) }}</option>
          </select>
        </div>
        <button class="btn btn-secondary" data-testid="carpool-refresh" :title="t('carpool.refresh')" :aria-label="t('carpool.refresh')" :disabled="loading" @click="loadCars">
          <Icon name="refresh" size="md" :class="{ 'animate-spin': loading }" />
        </button>
      </div>
      <p v-if="notice" role="status" class="text-sm text-emerald-700 dark:text-emerald-400">{{ t(notice) }}</p>
      <div v-if="uncertainCreate && mode === null" role="alert" class="flex flex-wrap items-center gap-3 text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.adminUI.createUncertain') }}<button class="btn btn-secondary" :disabled="loading" @click="reviewCreate">{{ t('carpool.adminUI.reviewCreate') }}</button></div>
      <div v-if="listError" role="alert" class="flex flex-wrap items-center gap-3 text-sm text-red-600 dark:text-red-400">
        <span>{{ t(listError) }}</span><button data-testid="carpool-list-retry" class="btn btn-secondary" @click="loadCars">{{ t('carpool.retry') }}</button>
      </div>
      <DataTable :columns="columns" :data="cars" :loading="loading">
        <template #cell-name="{ row }"><span class="inline-block max-w-40 whitespace-normal break-words font-medium [overflow-wrap:anywhere]">{{ row.name }}</span></template>
        <template #cell-status="{ row }"><CarpoolStatus :status="row.status" /></template>
        <template #cell-seats="{ row }"><dl class="space-y-1 whitespace-normal text-xs"><div v-for="item in [{ key: 'confirmed', value: row.confirmed_count }, { key: 'held', value: row.held_count }, { key: 'available', value: row.available_count }]" :key="item.key" class="flex justify-between gap-3"><dt class="text-gray-500">{{ t(`carpool.${item.key}`) }}</dt><dd class="tabular-nums">{{ item.value }}/{{ row.capacity }}</dd></div></dl></template>
        <template #cell-price_minor="{ row }">{{ formatCarpoolPrice(row.price_minor, locale) }}</template>
        <template #cell-times="{ row }">
          <dl class="space-y-1 whitespace-normal text-xs">
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('carpool.created') }}</dt><dd>{{ formatCarpoolTime(row.created_at, locale) }}</dd></div>
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('carpool.deadline') }}</dt><dd>{{ formatCarpoolTime(row.recruit_until, locale) }}</dd></div>
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('carpool.deliveryDue') }}</dt><dd>{{ formatCarpoolTime(row.delivery_due, locale) }}</dd></div>
          </dl>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex flex-wrap items-center gap-1">
            <button v-if="controlEnabled" :data-test="'node-' + row.id" class="action-button" :title="t('carpool.control.connection')" :aria-label="t('carpool.control.connection')" :disabled="busy" @click="nodeCar = row"><Icon name="link" size="sm" /></button>
            <button :data-test="'details-' + row.id" class="action-button" :title="t('carpool.details')" :aria-label="t('carpool.details')" :disabled="busy" @click="openDialog('details', row)"><Icon name="eye" size="sm" /></button>
            <button v-if="row.status === 'draft' && writable(row)" :data-test="'edit-' + row.id" class="action-button" :title="t('carpool.adminUI.edit')" :aria-label="t('carpool.adminUI.edit')" :disabled="busy" @click="openDialog('edit', row)"><Icon name="edit" size="sm" /></button>
            <button v-if="row.status === 'draft' && writable(row)" :data-test="'publish-' + row.id" class="action-button" :title="t('carpool.adminUI.publish')" :aria-label="t('carpool.adminUI.publish')" :disabled="busy" @click="openDialog('publish', row)"><Icon name="upload" size="sm" /></button>
            <button v-if="canEnroll(row, manualEnabled, now)" :data-test="'enroll-' + row.id" class="action-button" :title="t('carpool.manual.enroll')" :aria-label="t('carpool.manual.enroll')" :disabled="busy || loading" @click="enrollmentCar = row"><Icon name="plus" size="sm" /></button>
            <button v-if="cancellable(row)" :data-test="'cancel-' + row.id" class="action-button action-danger" :title="t('carpool.adminUI.cancelCar')" :aria-label="t('carpool.adminUI.cancelCar')" :disabled="busy" @click="openDialog('cancel', row)"><Icon name="x" size="sm" /></button>
            <button :data-test="'members-' + row.id" class="action-button" :title="t('carpool.adminUI.members')" :aria-label="t('carpool.adminUI.members')" :disabled="busy" @click="openMembers(row)"><Icon name="users" size="sm" /></button>
          </div>
        </template>
        <template #empty><p v-if="!listError">{{ t('carpool.noCars') }}</p></template>
      </DataTable>
      <Pagination :total="total" :page="page" :page-size="pageSize" @update:page="changePage" @update:page-size="changePageSize" />
      </div>
    </div>

    <BaseDialog :show="mode !== null" :title="dialogTitle" :width="mode === 'members' ? 'extra-wide' : 'wide'" :close-on-escape="!busy" :close-on-click-outside="false" :show-close-button="!busy" @close="closeDialog">
      <div class="min-w-0 space-y-4" :aria-busy="busy || dialogLoading">
        <p v-if="dialogLoading" role="status">{{ t('carpool.loading') }}</p>
        <div v-if="detailError" role="alert" class="space-y-2 text-sm text-red-600 dark:text-red-400">
          <p>{{ t(detailError) }}</p><button data-testid="carpool-detail-retry" class="btn btn-secondary" :disabled="busy || dialogLoading" @click="loadDetail">{{ t('carpool.retry') }}</button>
        </div>
        <p v-if="actionError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t(actionError) }}</p>
        <template v-if="selectedCar">
          <div class="flex min-w-0 flex-wrap items-center gap-3">
            <h2 class="break-words text-base font-semibold [overflow-wrap:anywhere]">{{ selectedCar.name }}</h2><CarpoolStatus :status="selectedCar.status" />
          </div>
          <p v-if="(mode === 'edit' || mode === 'publish') && selectedCar.status !== 'draft'" role="status">{{ t('carpool.adminUI.notDraft') }}</p>
          <p v-if="mode === 'cancel' && !cancellable(selectedCar)" role="status">{{ t('carpool.adminUI.notCancellable') }}</p>
        </template>
        <CarpoolCarForm v-if="mode === 'create' || (mode === 'edit' && selectedCar && selectedCar.status === 'draft')" :key="`${mode}-${selectedId}`" :car="selectedCar" :manual="mode === 'create' ? manualEnabled : selectedCar?.enrollment_mode === 'manual'" :now="now" :busy="busy" :disabled="!actionAllowed" @submit="save" />
        <template v-if="selectedCar && mode !== 'create' && mode !== 'edit' && mode !== 'members'">
          <CarpoolTerms :terms="selectedCar" />
          <dl class="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
            <div><dt class="text-gray-500">{{ t('carpool.confirmed') }}</dt><dd>{{ selectedCar.confirmed_count }}</dd></div>
            <div><dt class="text-gray-500">{{ t('carpool.held') }}</dt><dd>{{ selectedCar.held_count }}</dd></div>
            <div><dt class="text-gray-500">{{ t('carpool.created') }}</dt><dd>{{ formatCarpoolTime(selectedCar.created_at, locale) }}</dd></div>
            <div><dt class="text-gray-500">{{ t('carpool.published') }}</dt><dd>{{ formatCarpoolTime(selectedCar.published_at, locale) }}</dd></div>
            <div><dt class="text-gray-500">{{ t('carpool.deliveryDue') }}</dt><dd>{{ formatCarpoolTime(selectedCar.delivery_due, locale) }}</dd></div>
          </dl>
          <p v-if="mode === 'publish'" class="text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.adminUI.publishNotice') }}</p>
          <template v-if="mode === 'cancel'">
            <p class="text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.adminUI.cancelNotice') }}</p>
            <div>
              <label class="input-label" for="carpool-cancel-reason">{{ t('carpool.adminUI.cancelReason') }}</label>
              <textarea id="carpool-cancel-reason" v-model="reason" data-testid="carpool-cancel-reason" class="input" rows="3" required :disabled="!actionAllowed" :aria-describedby="cancelValidation ? 'carpool-cancel-error' : undefined" />
            </div>
            <label v-if="selectedCar.confirmed_count > 0" class="flex items-start gap-2 text-sm">
              <input v-model="confirmMembers" data-testid="carpool-confirm-members" type="checkbox" class="mt-1" :disabled="!actionAllowed" />
              <span>{{ t('carpool.adminUI.confirmMembers', { n: selectedCar.confirmed_count }) }}</span>
            </label>
            <p v-if="cancelValidation" id="carpool-cancel-error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t(cancelValidation) }}</p>
          </template>
        </template>
        <template v-if="mode === 'members' && selectedCar">
          <p class="text-sm font-medium text-amber-800 dark:text-amber-300">{{ t('carpool.testOrders') }}</p>
          <div v-if="memberError" role="alert" class="space-y-2 text-sm text-red-600 dark:text-red-400"><p>{{ t(memberError) }}</p><button data-testid="carpool-members-retry" class="btn btn-secondary" :disabled="memberLoading" @click="loadMembers">{{ t('carpool.retry') }}</button></div>
          <DataTable :columns="memberColumns" :data="members" :loading="memberLoading">
            <template #cell-id="{ row }"><span class="inline-block max-w-48 whitespace-normal break-all">{{ row.id }}</span></template>
            <template #cell-order_id="{ row }"><span class="inline-block max-w-48 whitespace-normal break-all">{{ row.order_id }}</span></template>
            <template #cell-status="{ row }"><CarpoolStatus :status="row.status" /></template>
            <template #cell-created_at="{ row }">{{ formatCarpoolTime(row.created_at, locale) }}</template>
            <template #cell-service="{ row }"><span v-if="!row.service_start">{{ t('carpool.serviceNotStarted') }}</span><span v-else class="whitespace-normal">{{ formatCarpoolTime(row.service_start, locale) }} / {{ formatCarpoolTime(row.service_end, locale) }}</span></template>
            <template #empty><p v-if="!memberError">{{ t('carpool.noMembers') }}</p></template>
          </DataTable>
          <Pagination :total="memberTotal" :page="memberPage" :page-size="memberPageSize" @update:page="changeMemberPage" @update:page-size="changeMemberPageSize" />
        </template>
      </div>
      <template #footer>
        <div class="flex flex-wrap justify-end gap-3">
          <button class="btn btn-secondary" :disabled="busy" @click="closeDialog">{{ t('carpool.close') }}</button>
          <button v-if="mode === 'create' && uncertainCreate" data-testid="carpool-review-create" class="btn btn-secondary" :disabled="busy || loading" @click="reviewCreate">{{ t('carpool.adminUI.reviewCreate') }}</button>
          <button v-if="mode === 'create' || mode === 'edit'" type="submit" form="carpool-car-form" data-testid="carpool-save" class="btn btn-primary" :disabled="!actionAllowed"><Icon :name="busy ? 'refresh' : 'check'" size="sm" class="mr-2" :class="{ 'animate-spin': busy }" />{{ t(busy ? 'carpool.processing' : actionError ? 'carpool.retry' : 'carpool.save') }}</button>
          <button v-if="mode === 'publish'" data-testid="carpool-publish-confirm" class="btn btn-primary" :disabled="!actionAllowed" @click="publish"><Icon name="upload" size="sm" class="mr-2" />{{ t(busy ? 'carpool.processing' : 'carpool.adminUI.publish') }}</button>
          <button v-if="mode === 'cancel'" data-testid="carpool-cancel-confirm" class="btn btn-danger" :disabled="!actionAllowed" @click="cancelCar"><Icon name="x" size="sm" class="mr-2" />{{ t(busy ? 'carpool.processing' : 'carpool.adminUI.cancelCar') }}</button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<style scoped>
.carpool-admin :deep(th), .carpool-admin :deep(td) { padding-left: 12px; padding-right: 12px; }
.action-button { @apply inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-md text-gray-600 hover:bg-gray-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 disabled:cursor-not-allowed disabled:opacity-50 dark:text-gray-300 dark:hover:bg-dark-700; }
.action-danger { @apply text-red-600 dark:text-red-400; }
</style>
