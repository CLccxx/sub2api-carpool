<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { carpoolAPI, type NativeSubscription, type NodeConsole, type NodeKey, type NodeUsage, type NodeUsageStats } from '@/api/carpool'
import { useCarpoolClock } from '@/composables/useCarpoolClock'
import { useAuthStore } from '@/stores/auth'
import { carpoolErrorKey, formatCarpoolTime, remainingSeconds } from '@/utils/carpool'
import { beginNodeKeyOperation, readNodeKeyOperation, finishNodeKeyOperation, clearNodeKeyJournals, type NodeKeyOperation } from '@/utils/carpoolKeyJournal'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean; carID: string; carName: string; enabled: boolean }>()
const emit = defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const auth = useAuthStore()
const open = ref(false)
const info = ref<NodeConsole>()
const tab = ref<'keys' | 'usage' | 'quota'>('keys')
const subscription = ref<NativeSubscription>()
const quotaLoading = ref(false)
const quotaError = ref('')
const clock = useCarpoolClock()
let quotaRequest = 0
let quotaTimer: ReturnType<typeof setInterval> | undefined
const loading = ref(false)
const error = ref('')
const busy = ref(false)
const actionError = ref('')
const needsRefresh = ref(false)
const keys = ref<NodeKey[]>([])
const keysLoading = ref(false)
const keysError = ref('')
const keyPage = ref(1)
const keySize = ref(20)
const keyTotal = ref(0)
const name = ref('')
const pending = ref<NodeKeyOperation | null>(null)
const secret = ref('')
const reveal = ref(false)
const copyNotice = ref('')
const editing = ref<number | null>(null)
const rename = ref('')
const deleting = ref<number | null>(null)
const start = ref('')
const end = ref('')
const usage = ref<NodeUsage[]>([])
const stats = ref<NodeUsageStats>()
const usageLoading = ref(false)
const usageLoaded = ref(false)
const usageError = ref('')
const usagePage = ref(1)
const usageSize = ref(20)
const usageTotal = ref(0)
let alive = true
let epoch = 0
let keyRequest = 0
let usageRequest = 0
let secretEpoch = 0
const available = computed(() => open.value && props.enabled && props.show && !!auth.user?.id)
const canRead = computed(() => available.value && !loading.value && !error.value && info.value?.can_read_node === true)
const canCreate = computed(() => canRead.value && info.value?.can_create_key === true && !busy.value && !needsRefresh.value)
const canMutate = computed(() => canRead.value && !busy.value && !needsRefresh.value && !keysLoading.value && !keysError.value)
const current = (ticket: number) => alive && ticket === epoch && available.value
const apiURL = computed(() => info.value?.native_subscription ? subscription.value?.api_base_url : info.value?.api_base_url)
const tabs = computed(() => info.value?.native_subscription ? ['keys', 'quota', 'usage'] as const : ['keys', 'usage'] as const)
const quotaState = computed(() => {
  const account = subscription.value?.account
  if (!account) return 'unavailable'
  if (account.state === 'ready' && ((account.observed_at && clock.now.value - Date.parse(account.observed_at) > 15 * 60000) || (account.resets_at && clock.now.value >= Date.parse(account.resets_at)))) return 'stale'
  return account.state
})
const resetCountdown = computed(() => {
  const reset = subscription.value?.account.resets_at
  if (!reset) return '-'
  const seconds = remainingSeconds(reset, clock.now.value)
  return [Math.floor(seconds / 3600), Math.floor(seconds / 60) % 60, seconds % 60].map(value => String(value).padStart(2, '0')).join(':')
})
const usd = (value: number | null | undefined) => value == null || !Number.isFinite(value) ? '-' : new Intl.NumberFormat(locale.value, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(value)
const validName = (value: string) => !!value.trim() && Array.from(value.trim()).length <= 100
const metadata = (key: NodeKey): NodeKey => ({ id: key.id, name: key.name, status: key.status, expires_at: key.expires_at })
function clearSecret() { secretEpoch++; secret.value = ''; reveal.value = false; copyNotice.value = '' }
function clearUsage() { usageRequest++; usage.value = []; stats.value = undefined; usageTotal.value = 0; usageLoading.value = false; usageLoaded.value = false; usageError.value = '' }
function reset() {
  quotaRequest++; clearInterval(quotaTimer); quotaTimer = undefined; subscription.value = undefined; quotaLoading.value = false; quotaError.value = ''
  epoch++; keyRequest++; clearSecret(); clearUsage()
  open.value = false; info.value = undefined; error.value = ''; loading.value = false; busy.value = false
  actionError.value = ''; needsRefresh.value = false; keys.value = []; keysLoading.value = false; keysError.value = ''
  keyPage.value = 1; keyTotal.value = 0; name.value = ''; pending.value = null; editing.value = null; deleting.value = null; rename.value = ''
  usagePage.value = 1; start.value = ''; end.value = ''; tab.value = 'keys'
}
function close() { reset(); emit('close') }
watch(() => [props.show, props.enabled, props.carID, auth.user?.id] as const, (next, previous) => {
  reset()
  if (previous && next[3] !== previous[3]) { clearNodeKeyJournals(); emit('close'); return }
  if (!next[0] || !next[1] || !next[2] || !next[3]) return
  open.value = true
  pending.value = readNodeKeyOperation(next[3], next[2]); name.value = pending.value?.name ?? ''
  void load()
}, { immediate: true, flush: 'sync' })
watch([start, end], () => { clearUsage(); usagePage.value = 1 }, { flush: 'sync' })
onUnmounted(() => { alive = false; reset() })

async function load() {
  if (!available.value || busy.value || loading.value) return
  const ticket = epoch
  loading.value = true; error.value = ''; clearSecret(); clearUsage(); keyRequest++; keys.value = []
  quotaRequest++; subscription.value = undefined; quotaError.value = ''; quotaLoading.value = false
  try {
    const result = await carpoolAPI.nodeConsole(props.carID)
    if (!current(ticket)) return
    info.value = result; needsRefresh.value = false
    if (!start.value) {
      const today = Date.parse(result.server_time)
      end.value = new Date(today).toISOString().slice(0, 10)
      start.value = new Date(today - 6 * 86400000).toISOString().slice(0, 10)
    }
    loading.value = false
    if (info.value.native_subscription) {
      void loadSubscription()
      quotaTimer ??= setInterval(() => { if (canRead.value && !quotaLoading.value) void loadSubscription() }, 60000)
    }
    if (tab.value === 'keys') await loadKeys(); else if (tab.value === 'usage') await loadUsage()
  } catch (err) { if (current(ticket)) { info.value = undefined; error.value = carpoolErrorKey(err) } }
  finally { if (current(ticket)) loading.value = false }
}
async function loadKeys() {
  if (!canRead.value) return
  const ticket = epoch
  const request = ++keyRequest
  keysLoading.value = true; keysError.value = ''; keys.value = []
  try {
    const result = await carpoolAPI.nodeKeys(props.carID, { page: keyPage.value, page_size: keySize.value })
    if (current(ticket) && request === keyRequest) { keys.value = result.items.map(metadata); keyTotal.value = result.total }
  } catch (err) { if (current(ticket) && request === keyRequest) { keysError.value = carpoolErrorKey(err); keyTotal.value = 0 } }
  finally { if (current(ticket) && request === keyRequest) keysLoading.value = false }
}
function changeKeyPage(value: number) { if (busy.value) return; clearSecret(); keyPage.value = value; void loadKeys() }
function changeKeySize(value: number) { keySize.value = Math.min(value, 100); changeKeyPage(1) }
function selectTab(value: 'keys' | 'usage' | 'quota') {
  if (tab.value === value) return
  clearSecret(); editing.value = null; deleting.value = null; tab.value = value
  if (value === 'usage') void loadUsage(); else if (value === 'quota') void loadSubscription(); else void loadKeys()
}
async function loadSubscription() {
  if (!canRead.value || !info.value?.native_subscription || quotaLoading.value) return
  const ticket = epoch; const request = ++quotaRequest; const started = performance.now()
  quotaLoading.value = true; quotaError.value = ''
  try {
    const value = await carpoolAPI.nodeSubscription(props.carID)
    if (current(ticket) && request === quotaRequest) { subscription.value = value; clock.sync(value.server_time, started) }
  } catch (err) {
    if (current(ticket) && request === quotaRequest) { subscription.value = undefined; quotaError.value = carpoolErrorKey(err) }
  } finally { if (current(ticket) && request === quotaRequest) quotaLoading.value = false }
}
async function create() {
  if (!canCreate.value || !auth.user?.id || !validName(pending.value?.name ?? name.value)) return
  const ticket = epoch
  clearSecret()
  const secretTicket = secretEpoch
  const attempt = beginNodeKeyOperation(auth.user.id, props.carID, name.value.trim())
  pending.value = attempt; name.value = attempt.name; busy.value = true; actionError.value = ''
  try {
    const result = await carpoolAPI.createNodeKey(attempt.car, attempt.name, attempt.operation)
    if (!current(ticket)) return
    // Keep metadata for an explicit recovery if the user left the key view mid-request.
    if (tab.value === 'keys' && secretTicket === secretEpoch && result.key) {
      secret.value = result.key; finishNodeKeyOperation(attempt); pending.value = null; name.value = ''
    }
    await loadKeys()
  } catch (err) {
    if (!current(ticket)) return
    const marker = (err as { reason?: string; code?: string })?.reason || (err as { code?: string })?.code
    if (marker === 'CARPOOL_KEY_GONE') { finishNodeKeyOperation(attempt); pending.value = null; actionError.value = 'carpool.control.keyGone' }
    else actionError.value = carpoolErrorKey(err)
  } finally { if (current(ticket)) busy.value = false }
}
function editKey(key: NodeKey) { if (canMutate.value) { clearSecret(); editing.value = key.id; rename.value = key.name; deleting.value = null } }
function requestDelete(id: number) { if (canMutate.value) { clearSecret(); deleting.value = id; editing.value = null } }
async function mutateKey(id: number, operation: 'rename' | 'toggle' | 'delete') {
  if (!canMutate.value) return
  const key = keys.value.find(item => item.id === id)
  if (!key || (operation === 'rename' && !validName(rename.value)) || (operation === 'delete' && deleting.value !== id)) return
  if (operation === 'toggle' && key.status !== 'active' && !canCreate.value) return
  const ticket = epoch
  const car = props.carID
  clearSecret(); busy.value = true; actionError.value = ''
  try {
    if (operation === 'delete') await carpoolAPI.deleteNodeKey(car, id)
    else await carpoolAPI.updateNodeKey(car, id, operation === 'rename' ? { name: rename.value.trim() } : { status: key.status === 'active' ? 'disabled' : 'active' })
    if (!current(ticket)) return
    editing.value = null; deleting.value = null
    await loadKeys()
  } catch (err) { if (current(ticket)) { actionError.value = carpoolErrorKey(err); needsRefresh.value = true } }
  finally { if (current(ticket)) busy.value = false }
}
function validDates() {
  const from = Date.parse(start.value); const to = Date.parse(end.value)
  return /^\d{4}-\d{2}-\d{2}$/.test(start.value) && /^\d{4}-\d{2}-\d{2}$/.test(end.value) &&
    Number.isFinite(from) && Number.isFinite(to) && new Date(from).toISOString().slice(0, 10) === start.value &&
    new Date(to).toISOString().slice(0, 10) === end.value && to >= from && to - from < 31 * 86400000
}
async function loadUsage() {
  if (!canRead.value) return
  clearUsage()
  if (!validDates()) { usageError.value = 'carpool.control.invalidDates'; return }
  const ticket = epoch
  const request = usageRequest
  const car = props.carID
  const range = { start_date: start.value, end_date: end.value }
  usageLoading.value = true
  try {
    const [result, summary] = await Promise.all([
      carpoolAPI.nodeUsage(car, { ...range, page: usagePage.value, page_size: usageSize.value }),
      carpoolAPI.nodeUsageStats(car, range)
    ])
    if (current(ticket) && request === usageRequest) { usage.value = result.items; usageTotal.value = result.total; stats.value = summary; usageLoaded.value = true }
  } catch { if (current(ticket) && request === usageRequest) usageError.value = 'carpool.control.usageUnavailable' }
  finally { if (current(ticket) && request === usageRequest) usageLoading.value = false }
}
function changeUsagePage(value: number) { usagePage.value = value; void loadUsage() }
function changeUsageSize(value: number) { usageSize.value = Math.min(value, 100); changeUsagePage(1) }
async function copy(value: string) {
  const ticket = epoch
  const secretTicket = secretEpoch
  try { await navigator.clipboard.writeText(value); if (current(ticket) && secretTicket === secretEpoch) copyNotice.value = 'carpool.control.copied' }
  catch { if (current(ticket) && secretTicket === secretEpoch) copyNotice.value = 'carpool.control.copyFailed' }
}
</script>

<template>
  <BaseDialog :show="available" :title="carName" width="wide" @close="close">
    <div class="min-w-0 space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3"><p v-if="info" class="text-sm font-medium" data-testid="console-access">{{ t(`carpool.control.access.${info.access_status}`) }}</p><button data-testid="console-refresh" class="btn btn-secondary" :disabled="loading || busy" :title="t('carpool.refresh')" :aria-label="t('carpool.refresh')" @click="load"><Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" /></button></div>
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('carpool.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(error) }}</p>
      <dl v-if="info" class="grid min-w-0 gap-3 text-sm sm:grid-cols-2">
        <div class="min-w-0 sm:col-span-2"><dt class="text-gray-500">{{ t('carpool.control.apiURL') }}</dt><dd class="flex min-w-0 items-start gap-2"><span data-testid="console-api-url" class="min-w-0 break-all font-mono text-xs">{{ apiURL || '-' }}</span><button v-if="apiURL" class="control-icon" :title="t('carpool.control.copy')" :aria-label="t('carpool.control.copy')" @click="copy(apiURL)"><Icon name="copy" size="sm" /></button></dd></div>
        <div><dt class="text-gray-500">{{ t('carpool.control.serviceStart') }}</dt><dd>{{ formatCarpoolTime(info.service_start, locale) }}</dd></div>
        <div><dt class="text-gray-500">{{ t('carpool.control.serviceEnd') }}</dt><dd>{{ formatCarpoolTime(info.service_end, locale) }}</dd></div>
      </dl>
      <div role="tablist" class="flex gap-4 border-b border-gray-200 dark:border-dark-700"><button v-for="item in tabs" :key="item" role="tab" :data-testid="`console-${item}-tab`" :aria-selected="tab === item" class="border-b-2 px-1 py-2 text-sm font-medium" :class="tab === item ? 'border-primary-500 text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500'" @click="selectTab(item)">{{ t(`carpool.control.${item}`) }}</button></div>
      <div v-if="tab === 'keys'" class="min-w-0 space-y-4">
        <form class="flex flex-wrap items-end gap-2" @submit.prevent="create"><div class="min-w-0 flex-1"><label for="console-key-name" class="input-label">{{ t('carpool.control.keyName') }}</label><input id="console-key-name" v-model="name" data-testid="console-name" class="input" :disabled="!canCreate || !!pending" maxlength="100" /></div><button data-testid="console-create" class="btn btn-primary" :disabled="!canCreate || !validName(name)"><Icon :name="pending ? 'refresh' : 'plus'" size="sm" />{{ t(pending ? 'carpool.control.recoverKey' : 'carpool.control.createKey') }}</button></form>
        <p v-if="pending" role="status" class="break-words text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.control.pendingKey') }} <span class="break-all font-mono text-xs">{{ pending.operation }}</span></p>
        <div v-if="secret" class="space-y-2 border-y border-emerald-200 py-3 dark:border-emerald-900">
          <label for="console-secret" class="input-label">{{ t('carpool.control.createdKey') }}</label><div class="flex min-w-0 gap-2"><input id="console-secret" :value="secret" data-testid="console-secret" :type="reveal ? 'text' : 'password'" readonly autocomplete="off" class="input min-w-0 flex-1 font-mono" /><button class="control-icon" :title="t(reveal ? 'carpool.control.hide' : 'carpool.control.reveal')" :aria-label="t(reveal ? 'carpool.control.hide' : 'carpool.control.reveal')" @click="reveal = !reveal"><Icon :name="reveal ? 'eyeOff' : 'eye'" size="sm" /></button><button class="control-icon" :title="t('carpool.control.copy')" :aria-label="t('carpool.control.copy')" @click="copy(secret)"><Icon name="copy" size="sm" /></button><button class="control-icon" :title="t('carpool.close')" :aria-label="t('carpool.close')" @click="clearSecret"><Icon name="x" size="sm" /></button></div>
        </div>
        <p v-if="keysLoading" role="status" class="text-sm text-gray-500">{{ t('carpool.loading') }}</p>
        <p v-else-if="keysError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(keysError) }} <button class="underline" @click="loadKeys">{{ t('carpool.retry') }}</button></p>
        <p v-else-if="canRead && !keys.length" class="text-sm text-gray-500">{{ t('carpool.control.noKeys') }}</p>
        <ul v-else class="divide-y divide-gray-100 dark:divide-dark-700">
          <li v-for="key in keys" :key="key.id" class="min-w-0 space-y-3 py-3">
            <div class="flex min-w-0 flex-wrap items-start justify-between gap-3"><div class="min-w-0 flex-1"><p class="break-words font-medium [overflow-wrap:anywhere]">{{ key.name }}</p><p class="text-xs text-gray-500">#{{ key.id }} · {{ t(`carpool.control.keyStatus.${key.status === 'active' ? 'active' : 'disabled'}`) }} · {{ formatCarpoolTime(key.expires_at, locale) }}</p><p class="font-mono text-xs text-gray-500">********</p></div><div class="flex shrink-0 gap-1">
              <button :data-testid="`console-rename-${key.id}`" class="control-icon" :disabled="!canMutate" :title="t('carpool.control.rename')" :aria-label="t('carpool.control.rename')" @click="editKey(key)"><Icon name="edit" size="sm" /></button>
              <button :data-testid="`console-toggle-${key.id}`" class="control-icon" :disabled="!canMutate || (key.status !== 'active' && !canCreate)" :title="t(key.status === 'active' ? 'carpool.control.disable' : 'carpool.control.enable')" :aria-label="t(key.status === 'active' ? 'carpool.control.disable' : 'carpool.control.enable')" @click="mutateKey(key.id, 'toggle')"><Icon :name="key.status === 'active' ? 'lock' : 'play'" size="sm" /></button>
              <button :data-testid="`console-delete-${key.id}`" class="control-icon text-red-600" :disabled="!canMutate" :title="t('carpool.control.delete')" :aria-label="t('carpool.control.delete')" @click="requestDelete(key.id)"><Icon name="trash" size="sm" /></button>
            </div></div>
            <form v-if="editing === key.id" class="flex min-w-0 flex-wrap gap-2" @submit.prevent="mutateKey(key.id, 'rename')"><input v-model="rename" data-testid="console-rename-input" :aria-label="t('carpool.control.keyName')" class="input min-w-0 flex-1" maxlength="100" :disabled="busy" /><button data-testid="console-rename-save" class="btn btn-primary" :disabled="!canMutate || !validName(rename)">{{ t('carpool.save') }}</button><button type="button" class="btn btn-secondary" :disabled="busy" @click="editing = null">{{ t('carpool.cancel') }}</button></form>
            <div v-if="deleting === key.id" class="space-y-2 text-sm"><p>{{ t('carpool.control.deleteConfirm') }}</p><div class="flex flex-wrap gap-2"><button data-testid="console-delete-confirm" class="btn btn-danger" :disabled="!canMutate" @click="mutateKey(key.id, 'delete')">{{ t('carpool.control.delete') }}</button><button class="btn btn-secondary" :disabled="busy" @click="deleting = null">{{ t('carpool.cancel') }}</button></div></div>
          </li>
        </ul>
        <Pagination v-if="canRead && keyTotal > 0" :total="keyTotal" :page="keyPage" :page-size="keySize" @update:page="changeKeyPage" @update:page-size="changeKeySize" />
      </div>
      <div v-else-if="tab === 'quota'" class="min-w-0 space-y-4">
        <p v-if="quotaLoading" role="status" class="text-sm text-gray-500">{{ t('carpool.loading') }}</p>
        <p v-if="quotaError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(quotaError) }} <button data-testid="console-quota-retry" class="underline" @click="loadSubscription">{{ t('carpool.retry') }}</button></p>
        <template v-if="subscription">
          <p class="break-words text-sm font-medium">{{ subscription.group_name }} <span class="font-normal text-gray-500">{{ t(`carpool.control.subscriptionStatus.${subscription.status}`) }}</span></p>
          <dl class="grid min-w-0 gap-4 border-y border-gray-200 py-4 text-sm dark:border-dark-700 sm:grid-cols-3">
            <div class="min-w-0"><dt class="text-gray-500">{{ t('carpool.control.weeklyUsed') }}</dt><dd data-testid="console-weekly-used" class="break-all font-semibold tabular-nums">{{ usd(subscription.weekly_used_usd) }}</dd></div>
            <div class="min-w-0"><dt class="text-gray-500">{{ t('carpool.control.weeklyLimit') }}</dt><dd data-testid="console-weekly-limit" class="break-all font-semibold tabular-nums">{{ usd(subscription.weekly_limit_usd) }}</dd></div>
            <div class="min-w-0"><dt class="text-gray-500">{{ t('carpool.control.weeklyRemaining') }}</dt><dd data-testid="console-weekly-remaining" class="break-all font-semibold tabular-nums">{{ usd(subscription.weekly_remaining_usd) }}</dd></div>
          </dl>
          <p role="status" class="text-sm" :class="quotaState === 'ready' ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-800 dark:text-amber-300'">{{ t(`carpool.control.quotaState.${quotaState}`) }}</p>
          <dl class="grid min-w-0 gap-4 text-sm sm:grid-cols-2">
            <div class="min-w-0"><dt class="text-gray-500">{{ t('carpool.control.accountUtilization') }}</dt><dd data-testid="console-account-utilization" class="font-semibold tabular-nums">{{ subscription.account.utilization == null ? '-' : `${subscription.account.utilization.toFixed(1)}%` }}</dd><progress v-if="subscription.account.utilization != null" :value="Math.min(subscription.account.utilization, 100)" max="100" :aria-label="t('carpool.control.accountUtilization')" class="mt-2 h-2 w-full accent-emerald-600" /></div>
            <div class="min-w-0"><dt class="text-gray-500">{{ t('carpool.control.accountEstimate') }}</dt><dd data-testid="console-account-estimate" class="break-all font-semibold tabular-nums">{{ usd(subscription.account.estimated_total) }}</dd></div>
            <div><dt class="text-gray-500">{{ t('carpool.control.accountReset') }}</dt><dd>{{ formatCarpoolTime(subscription.account.resets_at, locale) }}</dd><dd class="mt-1 text-xs text-gray-500">{{ t('carpool.control.resetCountdown') }} <span data-testid="console-reset-countdown" class="font-mono tabular-nums">{{ resetCountdown }}</span></dd></div>
            <div><dt class="text-gray-500">{{ t('carpool.control.observedAt') }}</dt><dd>{{ formatCarpoolTime(subscription.account.observed_at, locale) }}</dd></div>
            <div><dt class="text-gray-500">{{ t('carpool.control.quotaUpdatedAt') }}</dt><dd>{{ formatCarpoolTime(subscription.account.quota_updated_at, locale) }}</dd></div>
            <div><dt class="text-gray-500">{{ t('carpool.control.serviceEnd') }}</dt><dd>{{ formatCarpoolTime(subscription.expires_at, locale) }}</dd></div>
          </dl>
        </template>
      </div>
      <div v-else class="min-w-0 space-y-4">
        <form class="grid min-w-0 gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end" @submit.prevent="usagePage = 1; loadUsage()"><div class="min-w-0"><label for="console-start" class="input-label">{{ t('carpool.control.startDate') }}</label><input id="console-start" v-model="start" data-testid="console-start" type="date" class="input min-w-0 max-w-full" /></div><div class="min-w-0"><label for="console-end" class="input-label">{{ t('carpool.control.endDate') }}</label><input id="console-end" v-model="end" data-testid="console-end" type="date" class="input min-w-0 max-w-full" /></div><button data-testid="console-usage-load" class="btn btn-secondary" :disabled="!canRead || usageLoading" @click.prevent="usagePage = 1; loadUsage()"><Icon name="search" size="sm" />{{ t('carpool.control.query') }}</button></form>
        <p v-if="usageLoading" role="status" class="text-sm text-gray-500">{{ t('carpool.loading') }}</p>
        <p v-if="usageError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(usageError) }}</p>
        <dl v-if="stats" data-testid="console-usage-stats" class="grid gap-3 border-y border-gray-200 py-3 text-sm dark:border-dark-700 sm:grid-cols-3"><div><dt class="text-gray-500">{{ t('carpool.control.requests') }}</dt><dd class="font-semibold tabular-nums">{{ stats.total_requests }}</dd></div><div><dt class="text-gray-500">Tokens</dt><dd class="font-semibold tabular-nums">{{ stats.total_tokens }}</dd></div><div><dt class="text-gray-500">{{ t('carpool.control.cost') }}</dt><dd class="font-semibold tabular-nums">{{ stats.total_actual_cost.toFixed(6) }}</dd></div></dl>
        <p v-if="usageLoaded && !usage.length" class="text-sm text-gray-500">{{ t('carpool.control.noUsage') }}</p>
        <ul class="divide-y divide-gray-100 dark:divide-dark-700"><li v-for="row in usage" :key="row.id" class="grid min-w-0 gap-2 py-3 text-sm sm:grid-cols-2"><div class="min-w-0"><p class="break-all font-medium">{{ row.model }}</p><p class="text-xs text-gray-500">{{ formatCarpoolTime(row.created_at, locale) }} · Key #{{ row.api_key_id }}</p></div><div class="space-y-1 text-xs text-gray-500"><p>{{ t('carpool.control.inputTokens') }} {{ row.input_tokens }} / {{ t('carpool.control.outputTokens') }} {{ row.output_tokens }}</p><p>{{ t('carpool.control.cacheTokens') }} {{ row.cache_creation_tokens }} / {{ row.cache_read_tokens }}</p><p>{{ t('carpool.control.cost') }} {{ row.actual_cost.toFixed(6) }}</p></div></li></ul>
        <Pagination v-if="usageLoaded && usageTotal > 0" :total="usageTotal" :page="usagePage" :page-size="usageSize" @update:page="changeUsagePage" @update:page-size="changeUsageSize" />
      </div>
      <p v-if="actionError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(actionError) }}</p>
      <p v-if="needsRefresh" role="status" class="text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.control.reloadRequired') }}</p>
      <p v-if="copyNotice" role="status" class="text-sm text-gray-500">{{ t(copyNotice) }}</p>
    </div>
    <template #footer><button class="btn btn-secondary" @click="close">{{ t('carpool.close') }}</button></template>
  </BaseDialog>
</template>

<style scoped>
.control-icon { @apply inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-md text-gray-600 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-50 dark:text-gray-300 dark:hover:bg-dark-700; }
</style>
