<template>
  <div class="h-80">
    <v-chart :option="option" autoresize />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import VChart from './chart-core'

interface Row {
  user_id: number
  online_ip_count: number
}

const props = defineProps<{
  rows: Row[]
}>()

const option = computed(() => {
  const top = [...props.rows].sort((a, b) => b.online_ip_count - a.online_ip_count).slice(0, 10)
  return {
    tooltip: { trigger: 'axis' },
    grid: { left: 40, right: 20, top: 20, bottom: 40 },
    xAxis: {
      type: 'category',
      data: top.map((u) => `U${u.user_id}`),
    },
    yAxis: { type: 'value' },
    series: [
      {
        type: 'bar',
        data: top.map((u) => u.online_ip_count),
        itemStyle: { color: '#0ea5e9' },
      },
    ],
  }
})
</script>
