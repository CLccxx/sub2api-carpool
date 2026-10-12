<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CarInput } from '@/api/carpool'
import { formatCarpoolPrice, manualRefundPolicy, priceToMinor, seatPriceFromTotal } from '@/utils/carpool'

const props = defineProps<{ car?: CarInput; manual?: boolean; now: number; busy: boolean; disabled?: boolean }>()
const emit = defineEmits<{ submit: [input: CarInput] }>()
const { t, locale } = useI18n()
const form = reactive({ name: '', capacity: '2', price: '', serviceDays: '', deadline: '', deliveryHours: '', refundPolicy: '' })
const legacyPrice = computed(() => !!props.car && props.car.total_price_minor == null)
const seatPrice = computed(() => legacyPrice.value ? priceToMinor(form.price) : seatPriceFromTotal(priceToMinor(form.price), Number(form.capacity)))
type Field = keyof typeof form
const errors = reactive<Partial<Record<Field, string>>>({})
const bytes = (value: string) => new TextEncoder().encode(value).length
function localDate(value: string): string {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 19) : ''
}
watch(() => ({ car: props.car, manual: props.manual }), ({ car, manual }) => {
  const amount = car?.total_price_minor ?? car?.price_minor ?? 140000
  Object.assign(form, car ? {
    name: car.name, capacity: String(car.capacity),
    price: `${Math.floor(amount / 100)}.${String(amount % 100).padStart(2, '0')}`,
    serviceDays: String(car.service_days), deadline: localDate(car.recruit_until),
    deliveryHours: String(car.delivery_hours), refundPolicy: car.refund_policy
  } : { name: '', capacity: '2', price: '1400.00', serviceDays: '', deadline: '', deliveryHours: '', refundPolicy: '' })
  if (manual) form.refundPolicy = manualRefundPolicy
  for (const key of Object.keys(errors) as Field[]) delete errors[key]
}, { immediate: true })

function submit() {
  if (props.busy || props.disabled) return
  for (const key of Object.keys(errors) as Field[]) delete errors[key]
  const price = seatPrice.value
  const capacity = Number(form.capacity)
  const serviceDays = Number(form.serviceDays)
  const deliveryHours = Number(form.deliveryHours)
  const deadline = new Date(form.deadline)
  const latest = new Date(props.now)
  latest.setUTCFullYear(latest.getUTCFullYear() + 10)
  if (!form.name.trim() || bytes(form.name) > 160) errors.name = 'name'
  if (![2, 3, 4].includes(capacity)) errors.capacity = 'capacity'
  if (price === null) errors.price = 'price'
  if (!Number.isInteger(serviceDays) || serviceDays < 1 || serviceDays > 3650) errors.serviceDays = 'serviceDays'
  if (!Number.isInteger(deliveryHours) || deliveryHours < 1 || deliveryHours > 8760) errors.deliveryHours = 'deliveryHours'
  // The local round trip also rejects nonexistent times during a DST transition.
  if (!Number.isFinite(deadline.getTime()) || !localDate(form.deadline).startsWith(form.deadline) || deadline.getTime() <= props.now || deadline >= latest) errors.deadline = 'deadline'
  if (!form.refundPolicy.trim() || bytes(form.refundPolicy) > 4000) errors.refundPolicy = 'refundPolicy'
  if (Object.keys(errors).length) return
  emit('submit', {
    name: form.name.trim(), capacity, price_minor: price!, currency: 'CNY', service_days: serviceDays,
    ...(!legacyPrice.value ? { total_price_minor: priceToMinor(form.price)! } : {}),
    recruit_until: deadline.toISOString(), delivery_hours: deliveryHours, refund_policy: form.refundPolicy.trim()
  })
}
</script>

<template>
  <form id="carpool-car-form" novalidate :aria-busy="busy" @submit.prevent="submit">
    <fieldset :disabled="busy || disabled" class="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2">
      <div class="min-w-0 sm:col-span-2">
        <label class="input-label" for="carpool-name">{{ t('carpool.name') }}</label>
        <input id="carpool-name" v-model="form.name" data-testid="carpool-name" class="input" required :aria-invalid="!!errors.name" :aria-describedby="errors.name ? 'carpool-name-error' : undefined" />
        <p v-if="errors.name" id="carpool-name-error" role="alert" class="field-error">{{ t('carpool.adminUI.validation.name') }}</p>
      </div>
      <div class="min-w-0">
        <label class="input-label" for="carpool-capacity">{{ t('carpool.capacity') }}</label>
        <select id="carpool-capacity" v-model="form.capacity" data-testid="carpool-capacity" class="input" :aria-invalid="!!errors.capacity" :aria-describedby="errors.capacity ? 'carpool-capacity-error' : undefined">
          <option v-for="n in [2, 3, 4]" :key="n" :value="String(n)">{{ n }}</option>
        </select>
        <p v-if="errors.capacity" id="carpool-capacity-error" role="alert" class="field-error">{{ t('carpool.adminUI.validation.capacity') }}</p>
      </div>
      <div class="min-w-0">
        <label class="input-label" for="carpool-price">{{ t(legacyPrice ? 'carpool.price' : 'carpool.totalPrice') }}</label>
        <input id="carpool-price" v-model="form.price" data-testid="carpool-price" class="input" inputmode="decimal" required :aria-invalid="!!errors.price" :aria-describedby="errors.price ? 'carpool-price-error' : undefined" />
        <p v-if="errors.price" id="carpool-price-error" role="alert" class="field-error">{{ t('carpool.adminUI.validation.price') }}</p>
        <dl v-if="!legacyPrice" class="mt-2 flex flex-wrap justify-between gap-2 text-sm"><dt class="text-gray-500">{{ t('carpool.price') }}</dt><dd data-testid="carpool-seat-price-preview" class="font-medium tabular-nums">{{ seatPrice === null ? '-' : formatCarpoolPrice(seatPrice, locale) }}</dd></dl>
      </div>
      <div class="min-w-0">
        <label class="input-label" for="carpool-service-days">{{ t('carpool.adminUI.serviceDays') }}</label>
        <input id="carpool-service-days" v-model="form.serviceDays" data-testid="carpool-service-days" class="input" type="number" min="1" max="3650" step="1" required :aria-invalid="!!errors.serviceDays" :aria-describedby="errors.serviceDays ? 'carpool-service-days-error' : undefined" />
        <p v-if="errors.serviceDays" id="carpool-service-days-error" role="alert" class="field-error">{{ t('carpool.adminUI.validation.serviceDays') }}</p>
      </div>
      <div class="min-w-0">
        <label class="input-label" for="carpool-delivery-hours">{{ t('carpool.adminUI.deliveryHours') }}</label>
        <input id="carpool-delivery-hours" v-model="form.deliveryHours" data-testid="carpool-delivery-hours" class="input" type="number" min="1" max="8760" step="1" required :aria-invalid="!!errors.deliveryHours" :aria-describedby="errors.deliveryHours ? 'carpool-delivery-hours-error' : undefined" />
        <p v-if="errors.deliveryHours" id="carpool-delivery-hours-error" role="alert" class="field-error">{{ t('carpool.adminUI.validation.deliveryHours') }}</p>
      </div>
      <div class="min-w-0 sm:col-span-2">
        <label class="input-label" for="carpool-deadline">{{ t('carpool.deadline') }}</label>
        <input id="carpool-deadline" v-model="form.deadline" data-testid="carpool-deadline" class="input min-w-0 max-w-full" type="datetime-local" step="1" required :aria-invalid="!!errors.deadline" :aria-describedby="errors.deadline ? 'carpool-deadline-error' : undefined" />
        <p v-if="errors.deadline" id="carpool-deadline-error" role="alert" class="field-error">{{ t('carpool.adminUI.validation.deadline') }}</p>
      </div>
      <div class="min-w-0 sm:col-span-2">
        <label class="input-label" for="carpool-refund-policy">{{ t('carpool.refundPolicy') }}</label>
        <p v-if="manual" class="text-sm" data-testid="manual-refund-policy">{{ t('carpool.manual.policy') }}</p>
        <textarea v-else id="carpool-refund-policy" v-model="form.refundPolicy" data-testid="carpool-refund-policy" class="input" rows="4" required :aria-invalid="!!errors.refundPolicy" :aria-describedby="errors.refundPolicy ? 'carpool-refund-policy-error' : undefined" />
        <p v-if="errors.refundPolicy" id="carpool-refund-policy-error" role="alert" class="field-error">{{ t('carpool.adminUI.validation.refundPolicy') }}</p>
      </div>
    </fieldset>
  </form>
</template>

<style scoped>
.field-error { @apply mt-1 text-sm text-red-600 dark:text-red-400; }
</style>
