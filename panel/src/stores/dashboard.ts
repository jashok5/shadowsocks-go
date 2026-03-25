import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { fetchOverview, fetchUserDetail } from '../api/panel'
import type { OverviewResponse, UserDetailResponse } from '../types/panel'

interface TrafficPoint {
  time: string
  uploadRate: number
  downloadRate: number
}

export const useDashboardStore = defineStore('dashboard', () => {
  const loading = ref(false)
  const data = ref<OverviewResponse | null>(null)
  const lastUpdatedAt = ref<number>(0)
  const streamConnected = ref(false)
  const streamError = ref('')
  const detailLoading = ref(false)
  const selectedUser = ref<UserDetailResponse | null>(null)
  const trafficTrend = ref<TrafficPoint[]>([])
  const trendMaxPoints = 120
  let prevTotalUpload = 0
  let prevTotalDownload = 0
  let prevAt = 0
  let streamAbort: AbortController | null = null

  const hasData = computed(() => data.value !== null)

  async function refresh(): Promise<void> {
    loading.value = true
    try {
      const next = await fetchOverview()
      applyOverview(next)
    } finally {
      loading.value = false
    }
  }

  async function openUserDetail(userID: number): Promise<void> {
    detailLoading.value = true
    try {
      selectedUser.value = await fetchUserDetail(userID)
    } finally {
      detailLoading.value = false
    }
  }

  function clearUserDetail(): void {
    selectedUser.value = null
  }

  async function startStream(token: string): Promise<void> {
    stopStream()
    streamAbort = new AbortController()
    const url = '/panel/api/stream'
    try {
      const response = await fetch(url, {
        method: 'GET',
        headers: {
          Accept: 'text/event-stream',
          Authorization: `Bearer ${token}`,
        },
        signal: streamAbort.signal,
      })
      if (!response.ok || !response.body) {
        throw new Error(`stream status ${response.status}`)
      }
      streamConnected.value = true
      streamError.value = ''

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { value, done } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        const parts = buffer.split('\n\n')
        buffer = parts.pop() || ''
        for (const block of parts) {
          const evt = parseSSEBlock(block)
          if (evt.event === 'overview') {
            const next = JSON.parse(evt.data) as OverviewResponse
            applyOverview(next)
          }
        }
      }
      streamConnected.value = false
    } catch (error) {
      if (streamAbort?.signal.aborted) {
        streamConnected.value = false
        return
      }
      streamConnected.value = false
      streamError.value = error instanceof Error ? error.message : 'stream failed'
      throw error
    }
  }

  function stopStream(): void {
    if (streamAbort) {
      streamAbort.abort()
      streamAbort = null
    }
    streamConnected.value = false
  }

  async function fallbackPoll(): Promise<void> {
    await refresh()
  }

  function applyOverview(next: OverviewResponse): void {
    const now = Date.now()
    data.value = next
    lastUpdatedAt.value = now

    const curUpload = next.total_upload || 0
    const curDownload = next.total_download || 0
    let uploadRate = 0
    let downloadRate = 0

    if (prevAt > 0 && now > prevAt) {
      const seconds = (now - prevAt) / 1000
      const upDelta = curUpload - prevTotalUpload
      const downDelta = curDownload - prevTotalDownload
      if (upDelta >= 0) {
        uploadRate = upDelta / seconds
      }
      if (downDelta >= 0) {
        downloadRate = downDelta / seconds
      }
    }

    prevAt = now
    prevTotalUpload = curUpload
    prevTotalDownload = curDownload

    const point: TrafficPoint = {
      time: new Date(now).toLocaleTimeString('zh-CN', { hour12: false }),
      uploadRate,
      downloadRate,
    }
    const nextTrend = [...trafficTrend.value, point]
    trafficTrend.value = nextTrend.slice(-trendMaxPoints)
  }

  return {
    loading,
    data,
    hasData,
    lastUpdatedAt,
    streamConnected,
    streamError,
    detailLoading,
    selectedUser,
    trafficTrend,
    refresh,
    openUserDetail,
    clearUserDetail,
    startStream,
    stopStream,
    fallbackPoll,
  }
})

function parseSSEBlock(block: string): { event: string; data: string } {
  const lines = block.split('\n')
  let event = 'message'
  const dataLines: string[] = []
  for (const line of lines) {
    if (line.startsWith('event:')) {
      event = line.slice(6).trim()
      continue
    }
    if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trim())
    }
  }
  return { event, data: dataLines.join('\n') }
}
