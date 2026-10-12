<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { carpoolAPI, type ManualOrder, type ManualOrderQuery } from '@/api/carpool'
import { carpoolErrorKey, formatCarpoolPrice, formatCarpoolTime, localCarpoolDate, manualDate } from '@/utils/carpool'
import { useCarpoolClock } from '@/composables/useCarpoolClock'
import { useCarpoolManualCommand } from '@/composables/useCarpoolManualCommand'
import { useCarpoolRefresh } from '@/composables/useCarpoolRefresh'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import CarpoolStatus from './CarpoolStatus.vue'
import CarpoolPayment from './CarpoolPayment.vue'

const props = defineProps<{ carID?: string }>()
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const auth = useAuthStore()
const { now, sync } = useCarpoolClock()
const command = useCarpoolManualCommand(() => auth.user?.id, 'orders')
const { pending, busy, error } = command
const orders = ref<ManualOrder[]>([])
const carID = ref(props.carID || '')
const filter = ref<ManualOrderQuery['refund_status']>('')
const status = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loading = ref(false)
const listError = ref('')
const open = ref(false)
const detail = ref<ManualOrder | null>(null)
const action = ref<'detail' | 'withdraw' | 'refund'>('detail')
const detailLoading = ref(false)
const detailError = ref('')
const reason = ref('')
const refundedAt = ref('')
const reference = ref('')
const acknowledged = ref(false)
const validation = ref('')
const reconciled = ref(false)
const inspected = ref(false)
let ticket = 0
let detailTicket = 0
let alive = true
watch(() => props.carID, id => { carID.value = id || ''; page.value = 1; void load() })
async function load() {
  if (busy.value || !alive) return
  const current = ++ticket
  const owner = auth.user?.id
  const started = performance.now()
  loading.value = true; listError.value = ''
  try {
    const result = await carpoolAPI.manualOrders({ page: page.value, page_size: pageSize.value, car_id: carID.value || undefined, status: status.value || undefined, refund_status: filter.value || undefined })
    if (!alive || current !== ticket || owner !== auth.user?.id) return
    sync(result.server_time, started)
    const last = Math.max(1, Math.ceil(result.total / pageSize.value))
    if (page.value > last) { page.value = last; await load(); return }
    orders.value = result.items; total.value = result.total
  } catch (err) { if (alive && current === ticket && owner === auth.user?.id) { listError.value = carpoolErrorKey(err); orders.value = []; total.value = 0 } }
  finally { if (alive && current === ticket) loading.value = false }
}
function changePage(value: number) { page.value = value; void load() }
function changeSize(value: number) { pageSize.value = Math.min(100, Math.max(1, value)); changePage(1) }
async function show(orderID: string, next: typeof action.value = 'detail') {
  if (busy.value) return
  const current = ++detailTicket
  const owner = auth.user?.id
  const id = pending.value?.target || orderID
  action.value = pending.value && pending.value.action !== 'enroll' ? pending.value.action : next
  open.value = true; detail.value = null; detailLoading.value = true; detailError.value = ''; validation.value = ''; reason.value = ''; reference.value = ''; acknowledged.value = false; reconciled.value = false
  refundedAt.value = localCarpoolDate(now.value - 1000)
  try {
    const result = await carpoolAPI.manualOrder(id)
    if (alive && current === detailTicket && owner === auth.user?.id) detail.value = result
  } catch (err) { if (alive && current === detailTicket) detailError.value = carpoolErrorKey(err) }
  finally { if (alive && current === detailTicket) detailLoading.value = false }
}
function close() { if (!busy.value) { open.value = false; detailTicket++ } }
async function submit() {
  validation.value = ''
  if (!pending.value) {
    if (busy.value || detailLoading.value || !detail.value || !acknowledged.value) { validation.value = 'carpool.manual.validation'; return }
    if (action.value === 'withdraw') {
      if (detail.value.status !== 'confirmed' || !reason.value.trim() || new TextEncoder().encode(reason.value.trim()).length > 2000) { validation.value = 'carpool.adminUI.validation.cancelReason'; return }
    } else if (action.value === 'refund') {
      if (detail.value.payment?.refund?.status !== 'pending') { validation.value = 'carpool.manual.requestConflict'; return }
      if (!manualDate(refundedAt.value, now.value, Date.parse(detail.value.payment.received_at))) { validation.value = 'carpool.manual.invalidTime'; return }
      if (!/^[\x21-\x7e]{0,128}$/.test(reference.value.trim())) { validation.value = 'carpool.manual.invalidReference'; return }
    } else return
  }
  const result = await command.submit(pending.value ? undefined : action.value === 'withdraw'
    ? { action: 'withdraw', target: detail.value!.id, body: { reason: reason.value.trim(), confirm: true } }
    : { action: 'refund', target: detail.value!.id, body: { amount_minor: detail.value!.payment!.amount_minor, refunded_at: manualDate(refundedAt.value, now.value)!, reference: reference.value.trim(), confirm_refunded: true } })
  if (result) { close(); emit('changed'); await load() }
  else if (!pending.value && detail.value) { const id = detail.value.id; await show(id, action.value); await load() }
}
async function inspect() { if (!pending.value || busy.value) return; await show(pending.value.target, action.value); inspected.value = !detailError.value; await load() }
function finishReview() { command.reconcile(reconciled.value && inspected.value); if (!pending.value) close() }
watch(() => auth.user?.id, () => { ticket++; detailTicket++; orders.value = []; detail.value = null; open.value = false; void load() })
useCarpoolRefresh(load, () => { alive = false; ticket++; detailTicket++ })
</script>

<template>
  <section class="min-w-0 space-y-4" data-testid="manual-orders">
    <div class="flex flex-wrap items-end gap-3">
      <div><label for="manual-refund-filter" class="input-label">{{ t('carpool.manual.refundFilter') }}</label><select id="manual-refund-filter" v-model="filter" data-testid="manual-refund-filter" class="input" @change="changePage(1)"><option value="">{{ t('carpool.all') }}</option><option v-for="value in ['none', 'pending', 'refunded']" :key="value" :value="value">{{ t(`carpool.manual.${value}`) }}</option></select></div>
      <div><label for="manual-status-filter" class="input-label">{{ t('carpool.manual.status') }}</label><select id="manual-status-filter" v-model="status" class="input" @change="changePage(1)"><option value="">{{ t('carpool.all') }}</option><option value="confirmed">{{ t('carpool.manual.confirmed') }}</option><option value="cancelled">{{ t('carpool.status.cancelled') }}</option></select></div>
      <div class="min-w-0"><label for="manual-car-filter" class="input-label">{{ t('carpool.manual.carId') }}</label><input id="manual-car-filter" v-model="carID" data-testid="manual-car-filter" class="input" @change="changePage(1)" /></div>
      <button class="btn btn-secondary" :disabled="loading || busy" :aria-label="t('carpool.refresh')" :title="t('carpool.refresh')" @click="load"><Icon name="refresh" size="md" :class="{ 'animate-spin': loading }" /></button>
    </div>
    <div v-if="pending" role="status" class="flex flex-wrap items-center gap-3 text-sm text-amber-800 dark:text-amber-300"><span>{{ t('carpool.manual.retryUnknown') }}</span><button class="btn btn-secondary" data-testid="manual-order-resume" :disabled="busy" @click="show(pending.target)">{{ t('carpool.manual.recover') }}</button></div>
    <p v-if="listError" role="alert" class="text-sm text-red-600">{{ t(listError) }} <button class="btn btn-secondary" @click="load">{{ t('carpool.retry') }}</button></p>
    <p v-if="loading && !orders.length" role="status">{{ t('carpool.loading') }}</p><p v-else-if="!orders.length && !listError" class="py-10 text-center text-gray-500">{{ t('carpool.noOrders') }}</p>
    <div class="divide-y divide-gray-200 dark:divide-dark-700" :aria-busy="loading"><article v-for="order in orders" :key="order.id" class="min-w-0 space-y-3 py-4" :data-testid="`manual-order-${order.id}`">
      <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0"><h2 class="break-words text-base font-semibold [overflow-wrap:anywhere]">{{ order.snapshot.name }}</h2><p class="break-all text-sm">{{ order.user_email }} <span class="text-gray-500">#{{ order.user_id }} · {{ t('carpool.seat', { n: order.seat_no }) }}</span></p></div><CarpoolStatus :status="order.status" manual /></div>
      <CarpoolPayment v-if="order.payment" :payment="order.payment" />
      <div class="flex flex-wrap items-center justify-between gap-3"><p class="break-all font-mono text-xs text-gray-500">{{ order.id }}</p><div class="flex flex-wrap gap-2"><button class="btn btn-secondary" :disabled="busy" @click="show(order.id)">{{ t('carpool.details') }}</button><button v-if="order.status === 'confirmed'" :data-testid="`withdraw-${order.id}`" class="btn btn-secondary" :disabled="busy || loading" @click="show(order.id, 'withdraw')"><Icon name="x" size="sm" />{{ t('carpool.manual.withdraw') }}</button><button v-if="order.payment?.refund?.status === 'pending'" :data-testid="`refund-${order.id}`" class="btn btn-secondary" :disabled="busy || loading" @click="show(order.id, 'refund')"><Icon name="check" size="sm" />{{ t('carpool.manual.refund') }}</button></div></div>
    </article></div>
    <Pagination v-if="total" :total="total" :page="page" :page-size="pageSize" @update:page="changePage" @update:page-size="changeSize" />
    <BaseDialog :show="open" :title="t(action === 'detail' ? 'carpool.manual.detail' : `carpool.manual.${action}`)" width="wide" :close-on-escape="!busy" :show-close-button="!busy" @close="close">
      <div class="min-w-0 space-y-4"><p v-if="detailLoading" role="status">{{ t('carpool.loading') }}</p><p v-if="detailError" role="alert" class="text-sm text-red-600">{{ t(detailError) }}</p>
        <template v-if="detail"><h2 class="break-words font-semibold">{{ detail.snapshot.name }}</h2><p class="break-all text-sm">{{ detail.user_email }} (#{{ detail.user_id }}) · {{ t('carpool.seat', { n: detail.seat_no }) }}</p><CarpoolPayment v-if="detail.payment" :payment="detail.payment" />
          <dl class="grid min-w-0 gap-3 border-y border-gray-200 py-3 text-sm dark:border-dark-700 sm:grid-cols-2"><div><dt class="text-gray-500">{{ t('carpool.manual.reference') }}</dt><dd class="break-all">{{ detail.receipt.reference || '-' }}</dd></div><div><dt class="text-gray-500">{{ t('carpool.manual.receiptBy') }}</dt><dd>#{{ detail.receipt.recorded_by }}</dd></div><div class="min-w-0 sm:col-span-2"><dt class="text-gray-500">{{ t('carpool.manual.note') }}</dt><dd class="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{{ detail.receipt.note || '-' }}</dd></div><template v-if="detail.refund_record"><div class="min-w-0"><dt class="text-gray-500">{{ t('carpool.manual.reason') }}</dt><dd class="break-words [overflow-wrap:anywhere]">{{ detail.refund_record.reason }}</dd></div><div v-if="detail.refund_record.recorded_by"><dt class="text-gray-500">{{ t('carpool.manual.refundBy') }}</dt><dd>#{{ detail.refund_record.recorded_by }} · {{ formatCarpoolTime(detail.refund_record.recorded_at, locale) }}</dd></div><div v-if="detail.refund_record.reference"><dt class="text-gray-500">{{ t('carpool.manual.refundReference') }}</dt><dd class="break-all">{{ detail.refund_record.reference }}</dd></div></template></dl>
          <fieldset v-if="action !== 'detail' && !pending" :disabled="busy || detailLoading" class="space-y-4"><div v-if="action === 'withdraw'"><label for="manual-withdraw-reason" class="input-label">{{ t('carpool.manual.reason') }}</label><textarea id="manual-withdraw-reason" v-model="reason" data-testid="manual-withdraw-reason" class="input" rows="3" /></div><template v-else><p class="text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.manual.offlineOnly') }}</p><p class="font-semibold">{{ formatCarpoolPrice(detail.payment?.amount_minor || 0, locale) }}</p><div class="min-w-0"><label for="manual-refund-at" class="input-label">{{ t('carpool.manual.refundedAt') }}</label><input id="manual-refund-at" v-model="refundedAt" data-testid="manual-refund-at" type="datetime-local" step="1" :max="localCarpoolDate(now)" class="input min-w-0 max-w-full" /></div><div><label for="manual-refund-reference" class="input-label">{{ t('carpool.manual.refundReference') }}</label><input id="manual-refund-reference" v-model="reference" maxlength="128" class="input" /></div></template><label class="flex items-start gap-2 text-sm"><input v-model="acknowledged" type="checkbox" data-testid="manual-order-ack" class="mt-1" /><span>{{ t(action === 'withdraw' ? 'carpool.manual.withdrawConfirm' : 'carpool.manual.refundConfirm') }}</span></label></fieldset>
        </template>
        <p v-if="validation || error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t(validation || error) }}</p>
        <div v-if="pending?.uncertain" class="space-y-3 text-sm"><p class="text-amber-800 dark:text-amber-300">{{ t('carpool.manual.retryUnknown') }}</p><p class="break-all font-mono text-xs">{{ pending.key }}</p><button class="btn btn-secondary" :disabled="busy" @click="inspect">{{ t('carpool.manual.reconcile') }}</button><label v-if="inspected" class="flex items-start gap-2"><input v-model="reconciled" type="checkbox" class="mt-1" :disabled="busy" /><span>{{ t('carpool.manual.reconciled') }}</span></label><button v-if="inspected" class="btn btn-secondary" :disabled="busy || !reconciled" @click="finishReview">{{ t('carpool.manual.finishReview') }}</button></div>
      </div>
      <template #footer><div class="flex flex-wrap justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="close">{{ t('carpool.close') }}</button><button v-if="pending || action !== 'detail'" class="btn btn-primary" :data-testid="pending ? 'manual-retry' : 'manual-order-confirm'" :disabled="busy || (!pending && (detailLoading || !!detailError || !detail))" @click="submit"><Icon name="check" size="sm" />{{ t(busy ? 'carpool.processing' : pending ? 'carpool.manual.retry' : `carpool.manual.${action}`) }}</button></div></template>
    </BaseDialog>
  </section>
</template>
