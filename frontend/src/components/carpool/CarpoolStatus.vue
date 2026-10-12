<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
const props = defineProps<{ status: string; manual?: boolean }>()
const { t } = useI18n()
const color = computed(() => ({
  active: 'bg-emerald-50 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300',
  paused: 'bg-amber-50 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  revoked: 'bg-red-50 text-red-800 dark:bg-red-900/30 dark:text-red-300',
  recruiting: 'bg-emerald-50 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300',
  pending: 'bg-amber-50 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  needs_review: 'bg-amber-50 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  confirmed: 'bg-teal-50 text-teal-800 dark:bg-teal-900/30 dark:text-teal-300',
  waiting_deployment: 'bg-sky-50 text-sky-800 dark:bg-sky-900/30 dark:text-sky-300',
  awaiting_deployment: 'bg-sky-50 text-sky-800 dark:bg-sky-900/30 dark:text-sky-300'
}[props.status] || 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'))
</script>
<template><span class="inline-flex rounded px-2 py-1 text-xs font-medium" :class="color">{{ t(status === 'needs_review' ? 'carpool.manual.reviewCar' : manual && status === 'confirmed' ? 'carpool.manual.confirmed' : `carpool.status.${status}`) }}</span></template>
