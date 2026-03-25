<template>
  <div class="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
    <div
      v-for="card in cards"
      :key="card.label"
      :class="[
        'h-24 rounded-xl border bg-white/95 p-3 shadow-sm transition hover:-translate-y-0.5 hover:shadow',
        card.alert ? 'border-rose-200 bg-rose-50/40' : 'border-slate-200',
      ]"
    >
      <div class="flex items-start justify-between">
        <p class="text-xs tracking-wide text-slate-500">{{ card.label }}</p>
        <span
          class="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-slate-200 bg-slate-50 text-slate-600"
        >
          <n-icon size="14">
            <component :is="card.icon" />
          </n-icon>
        </span>
      </div>
      <p :class="['mt-2 truncate text-xl font-semibold leading-tight', card.alert ? 'text-rose-600' : 'text-slate-800']">
        {{ card.value }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { Component } from 'vue'

defineProps<{
  cards: Array<{ label: string; value: string; icon: Component; alert?: boolean }>
}>()
</script>
