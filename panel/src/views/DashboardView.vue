<template>
  <div class="min-h-screen bg-slate-100 text-slate-900">
    <header class="border-b border-slate-200 bg-white/90 backdrop-blur">
      <div class="mx-auto flex w-full max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-4">
        <div>
          <h1 class="text-xl font-semibold">节点状态面板</h1>
          <p class="text-xs text-slate-500">最后更新时间：{{ updatedText }}</p>
        </div>
        <n-space align="center" :size="12">
          <n-tag :type="dashboard.streamConnected ? 'success' : 'warning'" size="small">
            {{ dashboard.streamConnected ? 'SSE 已连接' : 'SSE 未连接' }}
          </n-tag>
          <n-button tertiary @click="showLogs = true">
            <template #icon>
              <n-icon><list-outline /></n-icon>
            </template>
            运行日志
          </n-button>
          <n-button :loading="dashboard.loading" type="primary" @click="manualRefresh">立即刷新</n-button>
          <n-button tertiary @click="logout">退出登录</n-button>
        </n-space>
      </div>
    </header>

    <main class="mx-auto w-full max-w-7xl space-y-4 px-4 py-6">
      <n-alert v-if="dashboard.streamError" type="warning" :title="`SSE 错误：${dashboard.streamError}`" />

      <kpi-tiles :cards="kpiCards" />

      <n-grid :cols="24" :x-gap="12" :y-gap="12" responsive="screen">
        <n-grid-item :span="24">
          <n-card title="节点实时流量" size="small">
            <component :is="TrafficTrendChart" :points="trafficTrend" />
          </n-card>
        </n-grid-item>
      </n-grid>

      <n-grid :cols="24" :x-gap="12" :y-gap="12" responsive="screen">
        <n-grid-item :span="24">
          <n-card title="用户实时流量 TOP10（上传+下载）" size="small">
            <component :is="OnlineTopBarChart" :rows="userRows" />
          </n-card>
        </n-grid-item>
      </n-grid>

      <n-grid :cols="24" :x-gap="12" :y-gap="12" responsive="screen">
        <n-grid-item :span="24" :l-span="14">
          <n-card title="用户连接与流量" size="small">
            <n-data-table
              :columns="userColumns"
              :data="userRows"
              :loading="dashboard.loading"
              size="small"
              :pagination="{ pageSize: 12 }"
              :bordered="false"
            />
          </n-card>
        </n-grid-item>

        <n-grid-item :span="24" :l-span="10">
          <n-card title="节点信息" size="small">
            <n-descriptions label-placement="left" :column="1" bordered size="small">
              <n-descriptions-item label="驱动">{{ data?.driver || '-' }}</n-descriptions-item>
              <n-descriptions-item label="版本">{{ data?.version || '-' }}</n-descriptions-item>
              <n-descriptions-item label="Go 版本">{{ data?.go_version || '-' }}</n-descriptions-item>
              <n-descriptions-item label="启动时间">{{ startedAtText }}</n-descriptions-item>
              <n-descriptions-item label="运行时长">{{ uptimeText }}</n-descriptions-item>
              <n-descriptions-item label="Goroutines">{{ memGoroutines }}</n-descriptions-item>
              <n-descriptions-item label="Heap Alloc">{{ memAllocText }}</n-descriptions-item>
              <n-descriptions-item label="Heap Inuse">{{ memInuseText }}</n-descriptions-item>
              <n-descriptions-item label="RSS 内存">{{ memRssText }}</n-descriptions-item>
              <n-descriptions-item label="GC 次数">{{ memNumGC }}</n-descriptions-item>
            </n-descriptions>
          </n-card>
        </n-grid-item>
      </n-grid>

      <n-grid v-if="showSSCard || showSSRCard" :cols="24" :x-gap="12" :y-gap="12" responsive="screen">
        <n-grid-item v-if="showSSCard" :span="24">
          <n-card title="SS 端口缓存/连接状态" size="small">
            <n-data-table
              :columns="ssColumns"
              :data="ssRows"
              :loading="dashboard.loading"
              size="small"
              :pagination="{ pageSize: 8 }"
              :bordered="false"
            />
          </n-card>
        </n-grid-item>
        <n-grid-item v-if="showSSRCard" :span="24">
          <n-card title="SSR 端口缓存/连接状态" size="small">
            <n-data-table
              :columns="ssrColumns"
              :data="ssrRows"
              :loading="dashboard.loading"
              size="small"
              :pagination="{ pageSize: 8 }"
              :bordered="false"
            />
          </n-card>
        </n-grid-item>
      </n-grid>

      <n-grid v-if="showATPCard && atpStat" :cols="24" :x-gap="12" :y-gap="12" responsive="screen">
        <n-grid-item :span="24">
          <n-card title="ATP 运行状态" size="small">
            <n-descriptions label-placement="left" :column="1" bordered size="small">
              <n-descriptions-item label="监听地址">{{ atpStat.listen }}:{{ atpStat.port }}</n-descriptions-item>
              <n-descriptions-item label="传输协议">{{ atpStat.transport || '-' }}</n-descriptions-item>
              <n-descriptions-item label="SNI">{{ atpStat.sni || '-' }}</n-descriptions-item>
              <n-descriptions-item label="代理状态">
                <n-tag :type="atpStat.proxy_active ? 'success' : 'error'" size="small">
                  {{ atpStat.proxy_active ? '运行中' : '未运行' }}
                </n-tag>
              </n-descriptions-item>
              <n-descriptions-item label="代理代数">{{ formatNumber(atpStat.proxy_generation || 0) }}</n-descriptions-item>
              <n-descriptions-item label="用户策略数">{{ formatNumber(atpStat.users || 0) }}</n-descriptions-item>
              <n-descriptions-item label="审计规则数">{{ formatNumber(atpStat.rules || 0) }}</n-descriptions-item>
              <n-descriptions-item label="证书到期">{{ atpCertNotAfterText }}</n-descriptions-item>
              <n-descriptions-item label="证书剩余">{{ atpCertRemainingText }}</n-descriptions-item>
              <n-descriptions-item label="最近应用">{{ atpLastApplyText }}</n-descriptions-item>
              <n-descriptions-item label="最近错误">{{ atpStat.last_error || '-' }}</n-descriptions-item>
            </n-descriptions>
          </n-card>
        </n-grid-item>
      </n-grid>
    </main>

    <user-detail-drawer v-model:show="showDetail" :user="userDetail" :loading="dashboard.detailLoading" />
    <logs-terminal-modal v-model:show="showLogs" />
  </div>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import {
  AlertCircleOutline,
  HardwareChipOutline,
  ListOutline,
  PeopleOutline,
  RadioOutline,
  ServerOutline,
  TrendingUpOutline,
} from '@vicons/ionicons5'
import { useAuthStore } from '../stores/auth'
import { useDashboardStore } from '../stores/dashboard'
import { formatBytes, formatDateTime, formatDuration, formatNumber } from '../utils/format'
import type { ATPStat, SSStat, SSRStat, UserOverview } from '../types/panel'
import KpiTiles from '../components/dashboard/KpiTiles.vue'
import UserDetailDrawer from '../components/dashboard/UserDetailDrawer.vue'
import LogsTerminalModal from '../components/logs/LogsTerminalModal.vue'

const TrafficTrendChart = defineAsyncComponent(() => import('../components/charts/TrafficTrendChart.vue'))
const OnlineTopBarChart = defineAsyncComponent(() => import('../components/charts/OnlineTopBarChart.vue'))

const message = useMessage()
const router = useRouter()
const auth = useAuthStore()
const dashboard = useDashboardStore()
const timer = ref<number | null>(null)
const reconnectTimer = ref<number | null>(null)
const showDetail = ref(false)
const showLogs = ref(false)

const data = computed(() => dashboard.data)
const userDetail = computed(() => dashboard.selectedUser)
const trafficTrend = computed(() => dashboard.trafficTrend)

const updatedText = computed(() => {
  if (!dashboard.lastUpdatedAt) return '-'
  return new Date(dashboard.lastUpdatedAt).toLocaleString('zh-CN')
})

const startedAtText = computed(() => {
  if (!data.value) return '-'
  return formatDateTime(data.value.started_at_unix)
})

const uptimeText = computed(() => {
  if (!data.value) return '-'
  return formatDuration(data.value.uptime_seconds)
})

const memGoroutines = computed(() => (data.value ? formatNumber(data.value.mem.goroutines) : '-'))
const memAllocText = computed(() => (data.value ? formatBytes(data.value.mem.heap_alloc) : '-'))
const memInuseText = computed(() => (data.value ? formatBytes(data.value.mem.heap_inuse) : '-'))
const memRssText = computed(() => (data.value ? formatBytes(data.value.mem.rss_bytes) : '-'))
const memNumGC = computed(() => (data.value ? formatNumber(data.value.mem.num_gc) : '-'))

const kpiCards = computed(() => [
  {
    label: '在线用户',
    value: data.value ? formatNumber(data.value.online_users) : '-',
    icon: PeopleOutline,
  },
  {
    label: '在线 IP',
    value: data.value ? formatNumber(data.value.online_ips) : '-',
    icon: RadioOutline,
  },
  {
    label: '监听端口',
    value: data.value ? formatNumber(data.value.ports) : '-',
    icon: ServerOutline,
  },
  {
    label: '异常 IP 数',
    value: data.value ? formatNumber(data.value.wrong_ips) : '-',
    icon: AlertCircleOutline,
    alert: (data.value?.wrong_ips || 0) > 0,
  },
  {
    label: 'Goroutines',
    value: data.value ? formatNumber(data.value.mem.goroutines) : '-',
    icon: TrendingUpOutline,
  },
  {
    label: 'RSS 内存',
    value: data.value ? formatBytes(data.value.mem.rss_bytes) : '-',
    icon: HardwareChipOutline,
  },
])

const userRows = computed<UserOverview[]>(() => data.value?.user_list || [])
const ssRows = computed<SSStat[]>(() => data.value?.ss_stats || [])
const ssrRows = computed<SSRStat[]>(() => data.value?.ssr_stats || [])
const atpStat = computed<ATPStat | null>(() => data.value?.atp_stats || null)
const showSSCard = computed(() => (data.value?.driver || '').toLowerCase() === 'ss')
const showSSRCard = computed(() => (data.value?.driver || '').toLowerCase() === 'ssr')
const showATPCard = computed(() => (data.value?.driver || '').toLowerCase() === 'atp')

const atpLastApplyText = computed(() => {
  if (!atpStat.value || !atpStat.value.last_apply_unix) return '-'
  return formatDateTime(atpStat.value.last_apply_unix)
})

const atpCertNotAfterText = computed(() => {
  if (!atpStat.value || !atpStat.value.cert_not_after_unix) return '-'
  return formatDateTime(atpStat.value.cert_not_after_unix)
})

const atpCertRemainingText = computed(() => {
  if (!atpStat.value || !atpStat.value.cert_remaining_sec) return '-'
  return formatDuration(atpStat.value.cert_remaining_sec)
})

const userColumns: DataTableColumns<UserOverview> = [
  { title: '用户ID', key: 'user_id', sorter: 'default' },
  {
    title: '在线IP',
    key: 'online_ips',
    sorter: (a, b) => (a.online_ips?.length || 0) - (b.online_ips?.length || 0),
    ellipsis: {
      tooltip: true,
    },
    render: (row) => {
      const ips = row.online_ips || []
      if (ips.length === 0) return '-'
      return ips.join(', ')
    },
  },
  { title: '上行', key: 'upload', render: (row) => formatBytes(row.upload) },
  { title: '下行', key: 'download', render: (row) => formatBytes(row.download) },
  {
    title: '最近连接',
    key: 'last_seen_unix',
    sorter: 'default',
    render: (row) => (row.last_seen_unix ? formatDateTime(row.last_seen_unix) : '-'),
  },
  { title: '规则触发', key: 'detect_count' },
  { title: '端口', key: 'ports', render: (row) => row.ports.join(', ') },
  {
    title: '操作',
    key: 'action',
    render: (row) =>
      h(
        NButton,
        {
          size: 'tiny',
          type: 'info',
          tertiary: true,
          onClick: () => {
            void openUser(row.user_id)
          },
        },
        { default: () => '详情' },
      ),
  },
]

const ssColumns: DataTableColumns<SSStat> = [
  { title: '端口', key: 'Port' },
  { title: 'Active TCP', key: 'ActiveTCP' },
  { title: 'TCP Drop', key: 'TCPDrop' },
  { title: 'UDP Drop', key: 'UDPDrop' },
  { title: 'Session Cache', key: 'UDPSessionCache', render: (row) => row.UDPSessionCache.Size },
  { title: 'Resolve Cache', key: 'UDPResolveCache', render: (row) => row.UDPResolveCache.Size },
  {
    title: '告警',
    key: 'UDPAssocAlerts',
    render: (row) =>
      h(
        NTag,
        { type: row.UDPAssocAlerts.Warn ? 'error' : 'success', size: 'small' },
        { default: () => (row.UDPAssocAlerts.Warn ? `错误突增 ${row.UDPAssocAlerts.ErrorDelta}` : '正常') },
      ),
  },
]

const ssrColumns: DataTableColumns<SSRStat> = [
  { title: '端口', key: 'Port' },
  {
    title: '在线用户数',
    key: 'UserOnlineCount',
    render: (row) => {
      const values = Object.values(row.UserOnlineCount || {})
      return values.reduce((sum, n) => sum + Number(n || 0), 0)
    },
  },
  { title: 'Active TCP', key: 'ActiveTCP', render: () => '-' },
  { title: 'TCP Drop', key: 'TCPDrop', render: () => '-' },
  { title: 'UDP Drop', key: 'UDPDrop', render: () => '-' },
  { title: 'Assoc Cache', key: 'UDPAssocCache', render: (row) => row.UDPAssocCache.Size },
  { title: 'Resolve Cache', key: 'UDPResolveCache', render: (row) => row.UDPResolveCache.Size },
  {
    title: '告警',
    key: 'UDPAssocAlerts',
    render: (row) =>
      h(
        NTag,
        { type: row.UDPAssocAlerts.Warn ? 'error' : 'success', size: 'small' },
        { default: () => (row.UDPAssocAlerts.Warn ? `错误突增 ${row.UDPAssocAlerts.ErrorDelta}` : '正常') },
      ),
  },
]

async function openUser(userID: number) {
  showDetail.value = true
  try {
    await dashboard.openUserDetail(userID)
  } catch {
    message.error('获取用户详情失败')
  }
}

async function refresh() {
  try {
    await dashboard.refresh()
  } catch {
    message.error('获取面板数据失败，请检查 token 或节点接口')
  }
}

function startPollTimer() {
  stopPollTimer()
  timer.value = window.setInterval(() => {
    if (!dashboard.streamConnected) {
      void dashboard.fallbackPoll()
    }
  }, 5000)
}

function stopPollTimer() {
  if (timer.value !== null) {
    window.clearInterval(timer.value)
    timer.value = null
  }
}

function scheduleReconnect() {
  if (reconnectTimer.value !== null) {
    window.clearTimeout(reconnectTimer.value)
  }
  reconnectTimer.value = window.setTimeout(() => {
    void connectStream()
  }, 2000)
}

async function connectStream() {
  if (!auth.token) return
  try {
    await dashboard.startStream(auth.token)
  } catch {
    scheduleReconnect()
  }
}

async function manualRefresh() {
  await refresh()
}

async function logout() {
  dashboard.stopStream()
  auth.logout()
  await router.replace('/login')
}

watch(showDetail, (v) => {
  if (!v) {
    dashboard.clearUserDetail()
  }
})

onMounted(async () => {
  await refresh()
  startPollTimer()
  void connectStream()
})

onUnmounted(() => {
  stopPollTimer()
  dashboard.stopStream()
  if (reconnectTimer.value !== null) {
    window.clearTimeout(reconnectTimer.value)
  }
})
</script>
