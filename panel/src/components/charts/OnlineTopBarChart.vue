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
  upload: number
  download: number
}

const props = defineProps<{
  rows: Row[]
}>()

const option = computed(() => {
  const top = [...props.rows]
    .sort((a, b) => b.upload + b.download - (a.upload + a.download))
    .slice(0, 10)
  return {
    tooltip: {
      trigger: 'axis',
      valueFormatter: (value: number) => formatBytes(Number(value)),
    },
    grid: { left: 40, right: 20, top: 20, bottom: 40 },
    xAxis: {
      type: 'category',
      data: top.map((u) => `U${u.user_id}`),
    },
    yAxis: {
      type: 'value',
      axisLabel: {
        formatter: (v: number) => formatBytes(v),
      },
    },
    series: [
      {
        type: 'bar',
        data: top.map((u) => u.upload + u.download),
        itemStyle: { color: '#0ea5e9' },
      },
    ],
  }
})

function formatBytes(v: number): string {
  if (!Number.isFinite(v) || v < 0) return '0 B'
  if (v >= 1024 ** 3) return `${(v / 1024 ** 3).toFixed(2)} GiB`
  if (v >= 1024 ** 2) return `${(v / 1024 ** 2).toFixed(2)} MiB`
  if (v >= 1024) return `${(v / 1024).toFixed(2)} KiB`
  return `${v.toFixed(0)} B`
}
</script>
