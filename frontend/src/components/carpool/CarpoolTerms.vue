<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CarInput } from '@/api/carpool'
import { formatCarpoolPrice, formatCarpoolTime } from '@/utils/carpool'
defineProps<{ terms: CarInput }>()
const { t, locale } = useI18n()
</script>
<template>
  <dl class="grid min-w-0 grid-cols-1 gap-4 text-sm sm:grid-cols-2">
    <div><dt class="text-gray-500">{{ t('carpool.price') }}</dt><dd class="mt-1 font-medium">{{ formatCarpoolPrice(terms.price_minor, locale) }}</dd></div>
    <div><dt class="text-gray-500">{{ t('carpool.capacity') }}</dt><dd class="mt-1">{{ terms.capacity }}</dd></div>
    <div><dt class="text-gray-500">{{ t('carpool.serviceDays') }}</dt><dd class="mt-1">{{ t('carpool.days', { n: terms.service_days }) }}</dd></div>
    <div><dt class="text-gray-500">{{ t('carpool.deliveryHours') }}</dt><dd class="mt-1">{{ t('carpool.hoursAfterFull', { n: terms.delivery_hours }) }}</dd></div>
    <div class="sm:col-span-2"><dt class="text-gray-500">{{ t('carpool.deadline') }}</dt><dd class="mt-1">{{ formatCarpoolTime(terms.recruit_until, locale) }}</dd></div>
    <div class="min-w-0 sm:col-span-2"><dt class="text-gray-500">{{ t('carpool.refundPolicy') }}</dt><dd class="mt-1 whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{{ terms.refund_policy }}</dd></div>
  </dl>
</template>
