<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import usersAPI from '@/api/admin/users'
import { carpoolAPI, type Car, type EnrollmentInput } from '@/api/carpool'
import type { AdminUser } from '@/types'
import { canEnroll, carpoolErrorKey, formatCarpoolPrice, localCarpoolDate, manualDate } from '@/utils/carpool'
import { useCarpoolClock } from '@/composables/useCarpoolClock'
import { useCarpoolManualCommand } from '@/composables/useCarpoolManualCommand'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean; initialCar?: Car; enabled: boolean }>()
const emit = defineEmits<{ close: []; changed: []; inspect: [carID: string] }>()
const { t, locale } = useI18n()
const auth = useAuthStore()
const { now, sync } = useCarpoolClock()
const command = useCarpoolManualCommand(() => auth.user?.id, 'enrollment')
const { pending, busy, error } = command
type User = Pick<AdminUser, 'id' | 'email' | 'status'>
const open = ref(false)
const car = ref<Car>()
const loading = ref(false)
const loadError = ref('')
const query = ref('')
const results = ref<User[]>([])
const selected = ref<User | null>(null)
const searching = ref(false)
const searched = ref(false)
const searchError = ref('')
const channel = ref<'alipay' | 'wechat'>('alipay')
const receivedAt = ref('')
const reference = ref('')
const note = ref('')
const acknowledged = ref(false)
const validation = ref('')
const reviewed = ref<EnrollmentInput | null>(null)
const reconciled = ref(false)
const inspected = ref(false)
let detailTicket = 0
let searchTicket = 0
let alive = true
const locked = computed(() => !!pending.value || busy.value)
const admissible = computed(() => !!car.value && !loading.value && !loadError.value && canEnroll(car.value, props.enabled, now.value))
const body = computed(() => pending.value?.action === 'enroll' ? pending.value.body : reviewed.value)

watch(query, () => { searchTicket++; selected.value = null; results.value = []; searched.value = false; searching.value = false; searchError.value = '' }, { flush: 'sync' })
async function load() {
  const target = pending.value?.target || props.initialCar?.id
  if (!target) return
  const ticket = ++detailTicket
  const started = performance.now()
  loading.value = true; loadError.value = ''
  try {
    const result = await carpoolAPI.getCar(target, true)
    if (!alive || ticket !== detailTicket) return
    car.value = result; sync(result.server_time, started)
    if (!receivedAt.value) receivedAt.value = localCarpoolDate(now.value - 1000)
    if (pending.value?.action === 'enroll') {
      const user = await usersAPI.getById(pending.value.body.user_id)
      if (alive && ticket === detailTicket) selected.value = user
    }
  } catch (err) { if (alive && ticket === detailTicket) loadError.value = carpoolErrorKey(err) }
  finally { if (alive && ticket === detailTicket) loading.value = false }
}
function resume() { open.value = true; reconciled.value = false; void load() }
watch(() => props.show, show => {
  if (!show) return
  open.value = true; query.value = ''; selected.value = null; reviewed.value = null; validation.value = ''; acknowledged.value = false
  channel.value = 'alipay'; receivedAt.value = ''; reference.value = ''; note.value = ''; car.value = undefined
  void load()
}, { immediate: true })
watch(() => auth.user?.id, () => { open.value = false; detailTicket++; searchTicket++; selected.value = null; car.value = undefined })
async function search() {
  if (locked.value || !query.value.trim()) return
  const ticket = ++searchTicket
  const text = query.value.trim()
  searching.value = true; searchError.value = ''; searched.value = false
  try {
    const page = await usersAPI.list(1, 20, { search: text, status: 'active' })
    if (alive && ticket === searchTicket) { results.value = page.items.filter(user => user.status === 'active'); searched.value = true }
  } catch (err) { if (alive && ticket === searchTicket) searchError.value = carpoolErrorKey(err) }
  finally { if (alive && ticket === searchTicket) searching.value = false }
}
function review() {
  validation.value = ''
  if (!admissible.value || !car.value || !selected.value || selected.value.status !== 'active' || !acknowledged.value) { validation.value = 'carpool.manual.validation'; return }
  const date = manualDate(receivedAt.value, now.value)
  if (!date) { validation.value = 'carpool.manual.invalidTime'; return }
  if (!/^[\x21-\x7e]{0,128}$/.test(reference.value.trim())) { validation.value = 'carpool.manual.invalidReference'; return }
  if (new TextEncoder().encode(note.value.trim()).length > 1000) { validation.value = 'carpool.manual.validation'; return }
  reviewed.value = { user_id: selected.value.id, amount_minor: car.value.price_minor, channel: channel.value, received_at: date, reference: reference.value.trim(), note: note.value.trim(), confirm_received: true }
}
async function submit() {
  if (!pending.value && (!admissible.value || !reviewed.value || !car.value)) return
  const result = await command.submit(pending.value ? undefined : { action: 'enroll', target: car.value!.id, body: reviewed.value! })
  if (result) { open.value = false; emit('close'); emit('changed') }
  else if (!pending.value) { reviewed.value = null; void load() }
}
function close() { if (!busy.value) { open.value = false; detailTicket++; searchTicket++; emit('close') } }
function inspect() { if (!pending.value || busy.value) return; inspected.value = true; const id = pending.value.target; close(); emit('inspect', id) }
function finishReview() { command.reconcile(reconciled.value && inspected.value); if (!pending.value) close() }
onUnmounted(() => { alive = false; detailTicket++; searchTicket++ })
</script>

<template>
  <div v-if="pending && !open" class="my-3 flex flex-wrap items-center gap-3 text-sm text-amber-800 dark:text-amber-300" role="status"><span>{{ t('carpool.manual.retryUnknown') }}</span><button class="btn btn-secondary" data-testid="manual-enroll-resume" @click="resume">{{ t('carpool.manual.recover') }}</button></div>
  <BaseDialog :show="open" :title="t('carpool.manual.enroll')" width="wide" :show-close-button="!busy" :close-on-escape="!busy" @close="close">
    <div class="min-w-0 space-y-4">
      <p v-if="loading" role="status">{{ t('carpool.loading') }}</p>
      <p v-if="loadError" role="alert" class="text-sm text-red-600">{{ t(loadError) }} <button class="btn btn-secondary" :disabled="busy" @click="load">{{ t('carpool.retry') }}</button></p>
      <h2 class="break-words text-base font-semibold [overflow-wrap:anywhere]">{{ car?.name || pending?.target }}</h2>
      <p v-if="!props.enabled && !pending" class="text-sm text-amber-800 dark:text-amber-300">{{ t('carpool.manual.disabled') }}</p>
      <template v-if="!reviewed && !pending">
        <fieldset :disabled="busy || loading" class="min-w-0 space-y-4">
          <div><label for="manual-user-search" class="input-label">{{ t('carpool.manual.search') }}</label><div class="flex min-w-0 gap-2"><input id="manual-user-search" v-model="query" data-testid="manual-user-search" class="input min-w-0" @keydown.enter.prevent="search" /><button class="btn btn-secondary shrink-0" data-testid="manual-user-search-submit" :title="t('carpool.manual.searchAction')" :aria-label="t('carpool.manual.searchAction')" :disabled="searching || !query.trim()" @click="search"><Icon name="search" size="md" /></button></div></div>
          <p v-if="searching" role="status" class="text-sm">{{ t('carpool.loading') }}</p><p v-if="searchError" role="alert" class="text-sm text-red-600">{{ t(searchError) }}</p><p v-if="searched && !results.length" class="text-sm text-gray-500">{{ t('carpool.manual.noUsers') }}</p>
          <div class="max-h-48 overflow-y-auto"><label v-for="user in results" :key="user.id" class="flex min-w-0 items-start gap-3 border-b border-gray-100 py-2 text-sm dark:border-dark-700"><input v-model="selected" :value="user" type="radio" name="manual-user" :data-testid="`manual-user-${user.id}`" class="mt-1" /><span class="min-w-0 break-all">{{ user.email }} <span class="text-gray-500">#{{ user.id }}</span></span></label></div>
          <p v-if="selected" class="break-all text-sm font-medium">{{ t('carpool.manual.identity') }}: {{ selected.email }} (#{{ selected.id }})</p>
          <div class="grid min-w-0 gap-4 sm:grid-cols-2"><div><span class="input-label">{{ t('carpool.manual.amount') }}</span><p data-testid="manual-amount" class="font-semibold">{{ formatCarpoolPrice(car?.price_minor || 0, locale) }}</p></div><div><label for="manual-channel" class="input-label">{{ t('carpool.manual.channel') }}</label><select id="manual-channel" v-model="channel" class="input"><option value="alipay">{{ t('carpool.manual.alipay') }}</option><option value="wechat">{{ t('carpool.manual.wechat') }}</option></select></div>
            <div class="min-w-0"><label for="manual-received-at" class="input-label">{{ t('carpool.manual.received') }}</label><input id="manual-received-at" v-model="receivedAt" data-testid="manual-received-at" type="datetime-local" step="1" :max="localCarpoolDate(now)" class="input min-w-0 max-w-full" /></div><div><label for="manual-reference" class="input-label">{{ t('carpool.manual.reference') }}</label><input id="manual-reference" v-model="reference" data-testid="manual-reference" maxlength="128" class="input" /></div></div>
          <div><label for="manual-note" class="input-label">{{ t('carpool.manual.note') }}</label><textarea id="manual-note" v-model="note" data-testid="manual-note" rows="2" class="input" /></div>
          <label class="flex items-start gap-2 text-sm"><input v-model="acknowledged" data-testid="manual-received-confirm" type="checkbox" class="mt-1" /><span>{{ t('carpool.manual.receivedConfirm') }}</span></label>
        </fieldset>
      </template>
      <dl v-if="body" class="grid min-w-0 gap-3 text-sm sm:grid-cols-2"><div class="min-w-0"><dt class="text-gray-500">{{ t('carpool.manual.identity') }}</dt><dd class="break-all">{{ selected?.email }} (#{{ body.user_id }})</dd></div><div><dt class="text-gray-500">{{ t('carpool.manual.amount') }}</dt><dd>{{ formatCarpoolPrice(body.amount_minor, locale) }} · {{ t(`carpool.manual.${body.channel}`) }}</dd></div><div><dt class="text-gray-500">{{ t('carpool.manual.received') }}</dt><dd class="break-all">{{ body.received_at }}</dd></div><div><dt class="text-gray-500">{{ t('carpool.manual.reference') }}</dt><dd class="break-all">{{ body.reference || '-' }}</dd></div></dl>
      <p v-if="validation || error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t(validation || error) }}</p>
      <div v-if="pending?.uncertain" class="space-y-3 border-t border-gray-200 pt-3 text-sm dark:border-dark-700"><p class="text-amber-800 dark:text-amber-300">{{ t('carpool.manual.retryUnknown') }}</p><p class="break-all font-mono text-xs">{{ t('carpool.manual.key') }}: {{ pending.key }}</p><button class="btn btn-secondary" :disabled="busy" @click="inspect">{{ t('carpool.manual.reconcile') }}</button><label v-if="inspected" class="flex items-start gap-2"><input v-model="reconciled" type="checkbox" :disabled="busy" class="mt-1" /><span>{{ t('carpool.manual.reconciled') }}</span></label><button v-if="inspected" class="btn btn-secondary" :disabled="busy || !reconciled" @click="finishReview">{{ t('carpool.manual.finishReview') }}</button></div>
    </div>
    <template #footer><div class="flex flex-wrap justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="close">{{ t('carpool.close') }}</button><button v-if="reviewed && !pending" class="btn btn-secondary" :disabled="busy" @click="reviewed = null">{{ t('carpool.manual.back') }}</button><button v-if="!reviewed && !pending" data-testid="manual-review" class="btn btn-primary" :disabled="!admissible" @click="review"><Icon name="check" size="sm" />{{ t('carpool.manual.review') }}</button><button v-else class="btn btn-primary" :data-testid="pending ? 'manual-retry' : 'manual-enroll-confirm'" :disabled="busy || (!pending && !admissible)" @click="submit"><Icon name="check" size="sm" />{{ t(busy ? 'carpool.processing' : pending ? 'carpool.manual.retry' : 'carpool.manual.confirmEnroll') }}</button></div></template>
  </BaseDialog>
</template>
