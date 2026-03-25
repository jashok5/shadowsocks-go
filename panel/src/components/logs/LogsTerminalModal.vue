<template>
  <n-modal
    v-model:show="showProxy"
    preset="card"
    title="节点运行日志"
    :style="{ width: '980px', maxWidth: '96vw' }"
    :bordered="false"
    segmented
  >
    <div class="terminal-modal-wrap">
      <div class="ops-panel">
        <div class="ops-head">
          <div class="ops-title-wrap">
            <p class="ops-title">Live Logs</p>
            <p class="ops-sub">{{ connected ? '流连接已建立' : '流连接中断，自动重连中' }}</p>
          </div>
          <div class="ops-stats">
            <span class="ops-pill">总条数 {{ logs.length }}</span>
            <span class="ops-pill">显示 {{ filteredLogs.length }}</span>
          </div>
        </div>

        <div class="ops-controls">
          <n-select
            v-model:value="levelFilter"
            :options="levelOptions"
            size="small"
            multiple
            clearable
            style="width: 240px"
            placeholder="日志级别过滤"
          />
          <n-select
            v-model:value="theme"
            :options="themeOptions"
            size="small"
            style="width: 130px"
          />
          <n-switch v-model:value="autoScroll" size="small">
            <template #checked>自动滚动</template>
            <template #unchecked>手动浏览</template>
          </n-switch>
          <n-button size="small" secondary @click="loadRecentLogs">加载最近400条</n-button>
          <n-button size="small" secondary @click="jumpToBottom">跳到底部</n-button>
          <n-button size="small" tertiary type="warning" @click="clearLogs">清空显示</n-button>
        </div>
      </div>

      <div
        ref="terminalRef"
        :class="['terminal-shell mt-4 min-h-0 flex-1 overflow-y-auto rounded-none border p-4 text-[13px] leading-6 shadow-inner', terminalThemeClass]"
        :style="terminalStyle"
        @scroll="onScroll"
        @wheel.passive="onWheel"
      >
        <div class="pointer-events-none mb-3 flex items-center gap-2 text-[11px]" :class="themeMetaClass">
          <span class="inline-block h-2 w-2 rounded-full bg-rose-400/70" />
          <span class="inline-block h-2 w-2 rounded-full bg-amber-400/70" />
          <span class="inline-block h-2 w-2 rounded-full bg-emerald-400/70" />
        </div>
        <div v-for="line in filteredLogs" :key="line.id" class="whitespace-pre-wrap wrap-break-word" :class="baseTextClass">
          <span :class="lineClass(line.text)">{{ line.text }}</span>
        </div>
        <div v-if="filteredLogs.length === 0" class="py-8 text-center" :class="themeEmptyClass">暂无匹配日志</div>
      </div>
    </div>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { fetchLogsSnapshot } from '../../api/logs'
import { useAuthStore } from '../../stores/auth'
import type { LogEntry } from '../../types/logs'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const auth = useAuthStore()
const showProxy = computed({
  get: () => props.show,
  set: (v: boolean) => emit('update:show', v),
})

const logs = ref<LogEntry[]>([])
const latestID = ref(0)
const connected = ref(false)
const autoScroll = ref(true)
const theme = ref<'terminal-black' | 'terminal-graphite'>('terminal-black')
const levelFilter = ref<Array<'error' | 'warn' | 'info' | 'debug' | 'other'>>(['error', 'warn', 'info', 'debug', 'other'])
const terminalRef = ref<HTMLElement | null>(null)
let streamAbort: AbortController | null = null

const themeOptions = [
  { label: '终端黑', value: 'terminal-black' },
  { label: '石墨灰', value: 'terminal-graphite' },
]

const levelOptions = [
  { label: 'Error', value: 'error' },
  { label: 'Warn', value: 'warn' },
  { label: 'Info', value: 'info' },
  { label: 'Debug', value: 'debug' },
  { label: 'Other', value: 'other' },
]

const terminalStyle = {
  fontFamily:
    'JetBrains Mono, Cascadia Code, SFMono-Regular, Menlo, Monaco, Consolas, Liberation Mono, monospace',
}

const terminalThemeClass = computed(() =>
  theme.value === 'terminal-black'
    ? 'terminal-black border-zinc-700'
    : 'terminal-graphite border-slate-700',
)

const baseTextClass = computed(() => (theme.value === 'terminal-black' ? 'text-white' : 'text-slate-100'))
const themeMetaClass = computed(() => (theme.value === 'terminal-black' ? 'text-zinc-500' : 'text-slate-500'))
const themeEmptyClass = computed(() => (theme.value === 'terminal-black' ? 'text-zinc-500' : 'text-slate-500'))
const filteredLogs = computed(() => {
  if (!levelFilter.value.length) {
    return logs.value
  }
  return logs.value.filter((line) => levelFilter.value.includes(detectLevel(line.text)))
})

async function open() {
  await startFromNow()
  await startStream()
}

function close() {
  stopStream()
}

async function loadSnapshot() {
  try {
    const data = await fetchLogsSnapshot(400)
    logs.value = data.items
    latestID.value = data.latest_id
    await scrollToBottom()
  } catch {
    logs.value = []
  }
}

async function loadRecentLogs() {
  autoScroll.value = false
  await loadSnapshot()
}

async function startFromNow() {
  logs.value = []
  try {
    const data = await fetchLogsSnapshot(1)
    latestID.value = data.latest_id
  } catch {
    latestID.value = 0
  }
}

async function startStream() {
  stopStream()
  streamAbort = new AbortController()
  const url = `/panel/api/logs/stream?after_id=${latestID.value}&limit=200`
  try {
    const response = await fetch(url, {
      method: 'GET',
      headers: {
        Accept: 'text/event-stream',
        Authorization: `Bearer ${auth.token}`,
      },
      signal: streamAbort.signal,
    })
    if (!response.ok || !response.body) {
      throw new Error(`stream status ${response.status}`)
    }
    connected.value = true
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
        if (evt.event === 'logs') {
          const payload = JSON.parse(evt.data) as { items: LogEntry[]; latest_id: number }
          appendLogs(payload.items)
          latestID.value = payload.latest_id
        }
      }
    }
    connected.value = false
  } catch {
    connected.value = false
    if (!streamAbort?.signal.aborted && showProxy.value) {
      window.setTimeout(() => {
        if (showProxy.value) {
          void startStream()
        }
      }, 1200)
    }
  }
}

function stopStream() {
  if (streamAbort) {
    streamAbort.abort()
    streamAbort = null
  }
  connected.value = false
}

function appendLogs(items: LogEntry[]) {
  if (!items.length) return
  logs.value = [...logs.value, ...items].slice(-1200)
  void scrollToBottom()
}

function clearLogs() {
  logs.value = []
}

async function jumpToBottom() {
  autoScroll.value = true
  await nextTick()
  const el = terminalRef.value
  if (!el) return
  el.scrollTop = el.scrollHeight
}

function onScroll() {
  const el = terminalRef.value
  if (!el) return
  if (el.scrollTop <= 2) {
    autoScroll.value = false
  }
  const distance = el.scrollHeight - (el.scrollTop + el.clientHeight)
  if (distance > 24) {
    autoScroll.value = false
  }
}

function onWheel(e: WheelEvent) {
  if (e.deltaY < 0) {
    autoScroll.value = false
  }
}

watch(theme, () => {
  void scrollToBottom()
})

async function scrollToBottom(force = false) {
  if (!autoScroll.value && !force) return
  await nextTick()
  const el = terminalRef.value
  if (!el) return
  el.scrollTop = el.scrollHeight
}

function lineClass(text: string) {
  const level = detectLevel(text)
  if (theme.value === 'terminal-black') {
    if (level === 'error') {
      return 'text-red-300'
    }
    if (level === 'warn') {
      return 'text-yellow-300'
    }
    if (level === 'info') {
      return 'text-emerald-300'
    }
    if (level === 'debug') {
      return 'text-sky-300'
    }
    return 'text-white'
  }
  if (level === 'error') {
    return 'text-rose-300'
  }
  if (level === 'warn') {
    return 'text-amber-300'
  }
  if (level === 'info') {
    return 'text-emerald-300'
  }
  if (level === 'debug') {
    return 'text-sky-300'
  }
  return 'text-slate-200'
}

function detectLevel(text: string): 'error' | 'warn' | 'info' | 'debug' | 'other' {
  const v = text.toLowerCase()
  if (v.includes(' error ') || v.includes('"level":"error"') || v.includes('level=error')) {
    return 'error'
  }
  if (v.includes(' warn ') || v.includes('"level":"warn"') || v.includes('level=warn')) {
    return 'warn'
  }
  if (v.includes(' info ') || v.includes('"level":"info"') || v.includes('level=info')) {
    return 'info'
  }
  if (v.includes(' debug ') || v.includes('"level":"debug"') || v.includes('level=debug')) {
    return 'debug'
  }
  return 'other'
}

watch(showProxy, (v) => {
  if (v) {
    void open()
  } else {
    close()
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
</script>

<style scoped>
.terminal-modal-wrap {
  height: 72vh;
  min-height: 420px;
  max-height: 72vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.ops-panel {
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: linear-gradient(180deg, #f8fafc 0%, #f1f5f9 100%);
  padding: 10px;
}

.ops-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}

.ops-title {
  margin: 0;
  font-size: 13px;
  font-weight: 700;
  color: #0f172a;
}

.ops-sub {
  margin: 2px 0 0;
  font-size: 12px;
  color: #475569;
}

.ops-stats {
  display: flex;
  align-items: center;
  gap: 6px;
}

.ops-pill {
  display: inline-flex;
  align-items: center;
  height: 24px;
  border-radius: 999px;
  padding: 0 10px;
  background: #ffffff;
  border: 1px solid #cbd5e1;
  color: #334155;
  font-size: 12px;
  font-weight: 600;
}

.ops-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.terminal-shell {
  background-size: 100% 24px;
  overscroll-behavior: contain;
}

.terminal-black {
  background-color: #050505;
  background-image: linear-gradient(rgba(255, 255, 255, 0.03) 1px, transparent 1px);
}

.terminal-graphite {
  background-color: #0f172a;
  background-image: linear-gradient(rgba(148, 163, 184, 0.06) 1px, transparent 1px);
}
</style>
