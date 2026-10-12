<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ManualPayment } from '@/api/carpool'
import { formatCarpoolPrice, formatCarpoolTime } from '@/utils/carpool'
defineProps<{ payment: ManualPayment }>()
const { t, locale } = useI18n()
</script>
<template>
  <dl class="grid min-w-0 grid-cols-1 gap-2 text-sm sm:grid-cols-2" data-testid="manual-payment">
    <div><dt class="text-gray-500">{{ t('carpool.manual.confirmed') }}</dt><dd>{{ formatCarpoolPrice(payment.amount_minor, locale) }} · {{ t(`carpool.manual.${payment.channel}`) }}</dd></div>
    <div><dt class="text-gray-500">{{ t('carpool.manual.received') }}</dt><dd>{{ formatCarpoolTime(payment.received_at, locale) }}</dd></div>
    <div><dt class="text-gray-500">{{ t('carpool.manual.recorded') }}</dt><dd>{{ formatCarpoolTime(payment.recorded_at, locale) }}</dd></div>
    <div v-if="payment.refund"><dt class="font-medium" :class="payment.refund.status === 'pending' ? 'text-amber-800 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">{{ t(`carpool.manual.${payment.refund.status}`) }}</dt><dd>{{ formatCarpoolPrice(payment.refund.amount_minor, locale) }}<span v-if="payment.refund.refunded_at"> · {{ formatCarpoolTime(payment.refund.refunded_at, locale) }}</span></dd></div>
  </dl>
</template>
