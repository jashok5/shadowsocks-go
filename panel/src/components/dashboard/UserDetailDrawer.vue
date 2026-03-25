<template>
  <n-drawer v-model:show="showProxy" :width="480" placement="right">
    <n-drawer-content :title="title" closable>
      <n-spin :show="loading">
        <n-descriptions label-placement="left" :column="1" bordered size="small">
          <n-descriptions-item label="用户ID">{{ user?.user_id ?? '-' }}</n-descriptions-item>
          <n-descriptions-item label="活跃">{{ user?.active ? '是' : '否' }}</n-descriptions-item>
          <n-descriptions-item label="上行">{{ user ? formatBytes(user.upload) : '-' }}</n-descriptions-item>
          <n-descriptions-item label="下行">{{ user ? formatBytes(user.download) : '-' }}</n-descriptions-item>
          <n-descriptions-item label="在线IP数">{{ user?.online_ip_count ?? '-' }}</n-descriptions-item>
          <n-descriptions-item label="端口">{{ user?.ports?.join(', ') || '-' }}</n-descriptions-item>
          <n-descriptions-item label="规则触发">{{ user?.detect_rules?.join(', ') || '-' }}</n-descriptions-item>
        </n-descriptions>
        <div class="mt-4">
          <p class="mb-2 text-sm font-medium text-slate-700">在线 IP 列表</p>
          <n-list bordered>
            <n-list-item v-for="ip in user?.online_ips || []" :key="ip">{{ ip }}</n-list-item>
            <n-list-item v-if="(user?.online_ips?.length || 0) === 0">暂无在线 IP</n-list-item>
          </n-list>
        </div>
      </n-spin>
    </n-drawer-content>
  </n-drawer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatBytes } from '../../utils/format'
import type { UserDetailResponse } from '../../types/panel'

const props = defineProps<{
  show: boolean
  user: UserDetailResponse | null
  loading: boolean
}>()

const emit = defineEmits<{
  'update:show': [boolean]
}>()

const showProxy = computed({
  get: () => props.show,
  set: (v: boolean) => emit('update:show', v),
})

const title = computed(() => `用户详情 #${props.user?.user_id || '-'}`)
</script>
