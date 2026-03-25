<template>
  <div class="h-80">
    <v-chart :option="option" autoresize />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import VChart from './chart-core'

interface Point {
  time: string
  uploadRate: number
  downloadRate: number
}

const props = defineProps<{
  points: Point[]
}>()

const option = computed(() => ({
  tooltip: { trigger: 'axis' },
  legend: { top: 8, data: ['上行', '下行'] },
  grid: { left: 48, right: 24, top: 48, bottom: 34 },
  xAxis: {
    type: 'category',
    boundaryGap: false,
    data: props.points.map((p) => p.time),
  },
  yAxis: {
    type: 'value',
    axisLabel: {
      formatter: (v: number) => {
        if (v >= 1024 * 1024) return `${(v / (1024 * 1024)).toFixed(1)} MiB/s`
        if (v >= 1024) return `${(v / 1024).toFixed(1)} KiB/s`
        return `${v.toFixed(0)} B/s`
      },
    },
  },
  series: [
    {
      name: '上行',
      type: 'line',
      smooth: true,
      showSymbol: false,
      areaStyle: { opacity: 0.08 },
      lineStyle: { width: 2, color: '#0ea5e9' },
      itemStyle: { color: '#0ea5e9' },
      data: props.points.map((p) => p.uploadRate),
    },
    {
      name: '下行',
      type: 'line',
      smooth: true,
      showSymbol: false,
      areaStyle: { opacity: 0.08 },
      lineStyle: { width: 2, color: '#10b981' },
      itemStyle: { color: '#10b981' },
      data: props.points.map((p) => p.downloadRate),
    },
  ],
}))
</script>
