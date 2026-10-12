<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { carpoolAPI, type NativeAccount, type NativeCarStatus, type NativeQuota, type NodeConnection, type NodeConnectionInput, type NodeSyncMember } from '@/api/carpool'
import { useAuthStore } from '@/stores/auth'
import { useStepUp, isStepUpCancelled, StepUpCancelledError } from '@/composables/useStepUp'
import { carpoolErrorKey, formatCarpoolTime } from '@/utils/carpool'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean; carID: string; carName: string; enabled: boolean }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const { t, locale } = useI18n()
const auth = useAuthStore()
const stepUp = useStepUp()
const open = ref(false)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const needsReload = ref(false)
const connection = ref<NodeConnection>()
const mainID = ref('')
const missing = ref(false)
const hostname = ref('')
const label = ref('')
const origin = ref('')
const instance = ref('')
const secret = ref('')
const members = ref<NodeSyncMember[]>([])
const syncLoading = ref(false)
const syncError = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const native = ref<NativeCarStatus>()
const accounts = ref<NativeAccount[]>([])
const account = ref(0)
const confirmActivation = ref(false)
const nativeError = ref('')
const nativeLoading = ref(false)
const quota = ref<NativeQuota>()
const quotaError = ref('')
const quotaLoading = ref(false)
let quotaRequest = 0
let epoch = 0
let syncRequest = 0
let nativeRequest = 0
let alive = true
let submitted: NodeConnectionInput | undefined
const available = computed(() => open.value && props.show && props.enabled && !!auth.user?.id)
const locked = computed(() => !available.value || busy.value || loading.value || needsReload.value)
const canSave = computed(() => !locked.value && !!connection.value && !!origin.value.trim() && (connection.value.credential_configured || !!secret.value.trim()))
const current = (ticket: number) => alive && ticket === epoch && available.value

function clearSecret() {
  secret.value = ''
  if (submitted) submitted.admin_key = ''
  submitted = undefined
}
function reset() {
  quotaRequest++; quota.value = undefined; quotaError.value = ''; quotaLoading.value = false
  epoch++; syncRequest++; nativeRequest++; stepUp.onCancel(); stepUp.blockedReason.value = ''; clearSecret()
  open.value = false; loading.value = false; busy.value = false; error.value = ''; notice.value = ''; needsReload.value = false
  connection.value = undefined; mainID.value = ''; missing.value = false
  hostname.value = ''; label.value = ''; origin.value = ''; instance.value = ''
  members.value = []; syncLoading.value = false; syncError.value = ''; page.value = 1; total.value = 0
  native.value = undefined; accounts.value = []; account.value = 0; confirmActivation.value = false; nativeError.value = ''; nativeLoading.value = false
}
function close() { reset(); emit('close') }
watch(() => [props.show, props.enabled, props.carID, auth.user?.id] as const, (next, previous) => {
  reset()
  if (previous && next[3] !== previous[3]) { emit('close'); return }
  if (next[0] && next[1] && next[2] && next[3]) { open.value = true; void load() }
}, { immediate: true, flush: 'sync' })
onUnmounted(() => { alive = false; reset() })

function accept(value: NodeConnection) {
  connection.value = value; missing.value = false
  origin.value = value.origin || `https://${value.hostname}`; instance.value = value.node_instance_id
}
async function load() {
  if (!available.value || busy.value || loading.value) return
  const ticket = epoch
  const car = props.carID
  loading.value = true; error.value = ''; clearSecret()
  try {
    const info = await carpoolAPI.controlInfo()
    if (!current(ticket)) return
    mainID.value = info.main_instance_id
    try {
      const value = await carpoolAPI.nodeConnection(car)
      if (!current(ticket)) return
      accept(value)
    } catch (err) {
      if (!current(ticket)) return
      if ((err as { status?: number })?.status !== 404) throw err
      connection.value = undefined; missing.value = true
    }
    needsReload.value = false
    if (connection.value) void loadSync()
    if (connection.value) await loadNative()
  } catch (err) {
    if (current(ticket)) { error.value = carpoolErrorKey(err); needsReload.value = true }
  } finally { if (current(ticket)) loading.value = false }
}
async function loadSync() {
  if (!available.value || !connection.value) return
  const ticket = epoch
  const request = ++syncRequest
  const car = props.carID
  syncLoading.value = true; syncError.value = ''; members.value = []
  try {
    const result = await carpoolAPI.nodeSync(car, { page: page.value, page_size: pageSize.value })
    if (current(ticket) && request === syncRequest) { members.value = result.items; total.value = result.total }
  } catch (err) { if (current(ticket) && request === syncRequest) { syncError.value = carpoolErrorKey(err); total.value = 0 } }
  finally { if (current(ticket) && request === syncRequest) syncLoading.value = false }
}
function changePage(value: number) { page.value = value; void loadSync() }
function changeSize(value: number) { pageSize.value = Math.min(value, 100); changePage(1) }

async function loadNative() {
  quotaRequest++; quota.value = undefined; quotaError.value = ''; quotaLoading.value = false
  const ticket = epoch
  const request = ++nativeRequest
  const valid = () => current(ticket) && request === nativeRequest
  const car = props.carID
  nativeLoading.value = true; nativeError.value = ''; confirmActivation.value = false
  try {
    const status = await carpoolAPI.nativeCarStatus(car)
    if (!valid()) return
    native.value = status; account.value = status.account_id
    const list = connection.value?.status === 'ready' ? await carpoolAPI.nativeAccounts(car) : []
    if (valid()) accounts.value = list
    if (valid() && status.group_id) await loadQuota()
  } catch (err) { if (valid()) { nativeError.value = carpoolErrorKey(err); native.value = undefined } }
  finally { if (valid()) nativeLoading.value = false }
}
async function prepare() {
  if (locked.value || nativeLoading.value || !account.value || connection.value?.status !== 'ready' || !native.value?.can_prepare) return
  const car = props.carID; const selected = Number(account.value); const ticket = epoch
  await write(() => carpoolAPI.prepareNativeCar(car, selected), value => { native.value = value; account.value = value.account_id })
  if (current(ticket)) { void loadSync(); void loadQuota() }
}
async function activate() {
  if (locked.value || nativeLoading.value || !native.value?.can_activate || !confirmActivation.value) return
  const car = props.carID; const ticket = epoch
  await write(() => carpoolAPI.activateNativeCar(car), value => { native.value = value; confirmActivation.value = false })
  if (current(ticket)) { void loadSync(); void loadQuota() }
}

async function loadQuota() {
  if (!available.value || !native.value?.group_id || connection.value?.status !== 'ready') return
  const ticket = epoch; const request = ++quotaRequest
  quotaLoading.value = true; quotaError.value = ''; quota.value = undefined
  try {
    const value = await carpoolAPI.nativeQuota(props.carID)
    if (current(ticket) && request === quotaRequest) quota.value = value
  } catch (err) { if (current(ticket) && request === quotaRequest) quotaError.value = carpoolErrorKey(err) }
  finally { if (current(ticket) && request === quotaRequest) quotaLoading.value = false }
}
async function syncQuota() {
  if (locked.value || quotaLoading.value || !native.value?.group_id || connection.value?.status !== 'ready') return
  const car = props.carID
  quotaRequest++; quota.value = undefined; quotaError.value = ''
  await write(() => carpoolAPI.syncNativeQuota(car), value => { quota.value = value })
}

async function write<T>(action: () => Promise<T>, acceptResult: (result: T) => void) {
  if (locked.value) return
  const ticket = epoch
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const result = await stepUp.run(async () => {
      if (!current(ticket)) throw new StepUpCancelledError()
      try { return await action() }
      catch (err) {
        // Do not let a late rejection reopen a dismissed step-up prompt.
        if (!current(ticket)) throw new StepUpCancelledError()
        throw err
      }
    })
    if (!current(ticket)) return
    notice.value = 'carpool.control.saved'; acceptResult(result); emit('changed')
  } catch (err) {
    if (current(ticket) && !isStepUpCancelled(err)) {
      error.value = stepUp.blockedReason.value ? 'carpool.control.stepUpBlocked' : carpoolErrorKey(err)
      needsReload.value = true
    }
  } finally { if (current(ticket)) { busy.value = false; clearSecret() } }
}
async function register() {
  if (!missing.value || !hostname.value.trim() || !label.value.trim()) return
  const input = { car_id: props.carID, hostname: hostname.value.trim(), label: label.value.trim() }
  const ticket = epoch
  await write(() => carpoolAPI.registerNode(input), () => { missing.value = false; needsReload.value = true })
  if (current(ticket) && !error.value) await load()
}
function save(toggle = false) {
  if (!connection.value || locked.value || (!toggle && !canSave.value)) return
  const value = connection.value
  const input: NodeConnectionInput = {
    origin: toggle ? value.origin : origin.value.trim(), node_instance_id: toggle ? value.node_instance_id : instance.value.trim(),
    admin_key: toggle ? '' : secret.value.trim(), expected_generation: value.generation,
    disabled: toggle ? value.status !== 'disabled' : value.status === 'disabled'
  }
  submitted = input
  void write(() => carpoolAPI.saveNodeConnection(value.node_id, input), accept)
}
function check() {
  if (!connection.value?.credential_configured || connection.value.status === 'disabled') return
  const id = connection.value.node_id
  const ticket = epoch
  void write(() => carpoolAPI.checkNodeConnection(id), accept).then(() => { if (current(ticket)) void loadNative() })
}
async function retrySync() {
  const car = props.carID
  const ticket = epoch
  await write(() => carpoolAPI.retryNodeSync(car), () => { notice.value = 'carpool.control.queued' })
  if (current(ticket)) void loadSync()
}
</script>

<template>
  <BaseDialog :show="available" :title="`${t('carpool.control.connection')} · ${carName}`" width="wide" @close="close">
    <div class="min-w-0 space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <span v-if="connection" class="text-sm font-medium">{{ t(`carpool.control.connectionStatus.${connection.status}`) }}</span>
        <button data-testid="node-refresh" class="btn btn-secondary" :disabled="loading || busy" :title="t('carpool.refresh')" :aria-label="t('carpool.refresh')" @click="load"><Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" /></button>
      </div>
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('carpool.loading') }}</p>
      <details><summary class="cursor-pointer text-sm text-gray-500">{{ t('carpool.control.technicalDetails') }}</summary><dl class="mt-3 grid min-w-0 gap-3 text-sm sm:grid-cols-2">
        <div><dt class="text-gray-500">{{ t('carpool.control.mainID') }}</dt><dd class="break-all font-mono text-xs">{{ mainID || '-' }}</dd></div>
        <div><dt class="text-gray-500">{{ t('carpool.control.carID') }}</dt><dd class="break-all font-mono text-xs">{{ carID }}</dd></div>
        <div v-if="connection"><dt class="text-gray-500">{{ t('carpool.control.nodeID') }}</dt><dd class="break-all font-mono text-xs">{{ connection.node_id }}</dd></div>
        <div v-if="connection"><dt class="text-gray-500">{{ t('carpool.control.generation') }}</dt><dd>{{ connection.generation }} / {{ connection.credential_mask || '-' }}</dd></div>
      </dl></details>
      <fieldset v-if="missing" :disabled="locked" class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-700">
        <div><label for="node-hostname" class="input-label">{{ t('carpool.control.hostname') }}</label><input id="node-hostname" v-model="hostname" data-testid="node-hostname" class="input" maxlength="253" autocomplete="off" /></div>
        <div><label for="node-label" class="input-label">{{ t('carpool.control.label') }}</label><input id="node-label" v-model="label" data-testid="node-label" class="input" maxlength="160" /></div>
        <button data-testid="node-register" class="btn btn-primary" :disabled="locked || !hostname.trim() || !label.trim()" @click="register"><Icon name="plus" size="sm" />{{ t('carpool.control.register') }}</button>
      </fieldset>
      <fieldset v-if="connection" :disabled="locked" class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-700">
        <div><label for="node-origin" class="input-label">{{ t('carpool.control.origin') }}</label><input id="node-origin" v-model="origin" data-testid="node-origin" class="input" type="url" autocomplete="off" /></div>
        <details><summary class="cursor-pointer text-sm text-gray-500">{{ t('carpool.control.instanceID') }}</summary><input id="node-instance" v-model="instance" :aria-label="t('carpool.control.instanceID')" data-testid="node-instance" class="input mt-2 font-mono" autocomplete="off" /></details>
        <div><label for="node-secret" class="input-label">{{ t('carpool.control.credential') }}</label><input id="node-secret" v-model="secret" data-testid="node-secret" class="input" type="password" autocomplete="new-password" :placeholder="connection.credential_configured ? t('carpool.control.keepCredential') : ''" /></div>
        <div class="flex flex-wrap gap-2">
          <button data-testid="node-save" class="btn btn-primary" :disabled="!canSave" @click="save()"><Icon name="check" size="sm" />{{ t('carpool.save') }}</button>
          <button data-testid="node-check" class="btn btn-secondary" :disabled="locked || !connection.credential_configured || connection.status === 'disabled'" @click="check"><Icon name="link" size="sm" />{{ t('carpool.control.check') }}</button>
          <button data-testid="node-toggle" class="btn btn-secondary" :disabled="locked || !connection.credential_configured" @click="save(true)"><Icon :name="connection.status === 'disabled' ? 'play' : 'lock'" size="sm" />{{ t(connection.status === 'disabled' ? 'carpool.control.enable' : 'carpool.control.disable') }}</button>
        </div>
      </fieldset>
      <p v-if="error" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(error) }}</p>
      <p v-if="needsReload" role="status" class="text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.control.reloadRequired') }}</p>
      <p v-if="notice" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">{{ t(notice) }}</p>
      <fieldset v-if="connection" :disabled="locked || nativeLoading || connection.status !== 'ready'" class="min-w-0 space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
        <h3 class="text-sm font-semibold">{{ t('carpool.control.nativeSetup') }}</h3>
        <p v-if="nativeError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(nativeError) }}</p>
        <div><label for="node-account" class="input-label">{{ t('carpool.control.upstreamAccount') }}</label><select id="node-account" v-model.number="account" data-testid="node-account" class="input" :disabled="!!native?.account_id"><option :value="0">{{ t('carpool.control.selectAccount') }}</option><option v-for="item in accounts" :key="item.id" :value="item.id">{{ item.name }} ({{ item.platform }})</option></select></div>
        <p v-if="native?.group_id" class="text-sm">{{ t('carpool.control.nativeGroup') }}: {{ native.group_id }}</p>
        <button data-testid="node-prepare" class="btn btn-secondary" :disabled="locked || nativeLoading || !account || !native?.can_prepare || !!nativeError || connection.status !== 'ready'" @click="prepare"><Icon name="sync" size="sm" />{{ t('carpool.control.prepareNative') }}</button>
        <label v-if="!native?.service_start" class="flex items-start gap-2 text-sm"><input v-model="confirmActivation" data-testid="node-activate-confirm" type="checkbox" class="mt-1" :disabled="!native?.can_activate" />{{ t('carpool.control.confirmActivation') }}</label>
        <button data-testid="node-activate" class="btn btn-primary" :disabled="locked || nativeLoading || !native?.can_activate || !confirmActivation || !!nativeError || connection.status !== 'ready'" @click="activate"><Icon name="play" size="sm" />{{ t('carpool.control.activateNative') }}</button>
        <dl v-if="native?.service_start" class="grid gap-2 text-sm sm:grid-cols-2"><div><dt class="text-gray-500">{{ t('carpool.control.serviceStart') }}</dt><dd>{{ formatCarpoolTime(native.service_start, locale) }}</dd></div><div><dt class="text-gray-500">{{ t('carpool.control.serviceEnd') }}</dt><dd>{{ formatCarpoolTime(native.service_end, locale) }}</dd></div></dl>
        <section v-if="native?.group_id" class="min-w-0 space-y-3 border-t border-gray-200 pt-3 dark:border-dark-700">
          <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="text-sm font-semibold">{{ t('carpool.control.quota') }}</h3><button data-testid="node-quota-sync" class="btn btn-secondary" :disabled="locked || quotaLoading || connection.status !== 'ready'" @click="syncQuota"><Icon name="sync" size="sm" />{{ t('carpool.control.syncQuota') }}</button></div>
          <p v-if="quotaLoading" role="status" class="text-sm text-gray-500">{{ t('carpool.loading') }}</p>
          <p v-if="quotaError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(quotaError) }}</p>
          <template v-if="quota">
            <p class="text-sm" :class="quota.state === 'ready' ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-800 dark:text-amber-300'">{{ t(`carpool.control.quotaState.${quota.state}`) }}</p>
            <dl class="grid min-w-0 gap-3 text-sm sm:grid-cols-2">
              <div class="min-w-0"><dt class="text-gray-500">{{ t('carpool.control.weeklyLimit') }}</dt><dd data-testid="node-weekly-limit" class="break-all font-semibold tabular-nums">{{ quota.weekly_limit_usd.toFixed(2) }}</dd></div>
              <div><dt class="text-gray-500">{{ t('carpool.control.accountReset') }}</dt><dd>{{ formatCarpoolTime(quota.resets_at, locale) }}</dd></div>
              <div><dt class="text-gray-500">{{ t('carpool.control.observedAt') }}</dt><dd>{{ formatCarpoolTime(quota.observed_at, locale) }}</dd></div>
              <div><dt class="text-gray-500">{{ t('carpool.control.quotaUpdatedAt') }}</dt><dd>{{ formatCarpoolTime(quota.quota_updated_at, locale) }}</dd></div>
            </dl>
          </template>
        </section>
      </fieldset>
      <div v-if="connection" class="min-w-0 space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="text-sm font-semibold">{{ t('carpool.control.sync') }}</h3><button data-testid="node-retry-sync" class="btn btn-secondary" :disabled="locked || connection.status !== 'ready'" @click="retrySync"><Icon name="sync" size="sm" />{{ t('carpool.control.retrySync') }}</button></div>
        <p v-if="connection.last_error_code" class="break-all text-xs text-red-700 dark:text-red-300">{{ connection.last_error_code }}</p>
        <p v-if="syncLoading" role="status" class="text-sm text-gray-500">{{ t('carpool.loading') }}</p>
        <p v-else-if="syncError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t(syncError) }} <button class="underline" @click="loadSync">{{ t('carpool.retry') }}</button></p>
        <p v-else-if="!members.length" class="text-sm text-gray-500">{{ t('carpool.noMembers') }}</p>
        <ul v-else class="divide-y divide-gray-100 dark:divide-dark-700">
          <li v-for="member in members" :key="member.membership_id" class="grid min-w-0 gap-2 py-3 text-sm sm:grid-cols-2">
            <div class="min-w-0"><p>{{ t('carpool.adminUI.userId') }}: {{ member.user_id }}</p><p class="break-all font-mono text-xs text-gray-500">{{ member.membership_id }}</p></div>
            <div class="space-y-1"><p>{{ t(`carpool.control.grant.${member.status}`) }} / {{ t(`carpool.control.delivery.${member.delivery}`) }}</p><p class="text-xs text-gray-500">{{ t('carpool.control.version') }} {{ member.ack_version }}/{{ member.version }} · {{ t('carpool.control.attempts') }} {{ member.attempts }}</p><p v-if="member.last_error_code" class="break-all text-xs text-red-700 dark:text-red-300">{{ member.last_error_code }}</p><p class="text-xs text-gray-500">{{ formatCarpoolTime(member.synced_at, locale) }}</p></div>
          </li>
        </ul>
        <Pagination v-if="total > 0" :total="total" :page="page" :page-size="pageSize" @update:page="changePage" @update:page-size="changeSize" />
      </div>
    </div>
    <template #footer><button class="btn btn-secondary" @click="close">{{ t('carpool.close') }}</button></template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>
