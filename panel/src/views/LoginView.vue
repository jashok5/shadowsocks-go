<template>
  <div class="min-h-screen bg-slate-950 px-4 py-10 text-slate-100">
    <div class="mx-auto flex min-h-[calc(100vh-5rem)] w-full max-w-5xl items-center justify-center">
      <div class="grid w-full overflow-hidden rounded-2xl border border-slate-800 bg-slate-900 shadow-2xl md:grid-cols-2">
        <section class="hidden bg-linear-to-br from-cyan-500 via-sky-600 to-blue-700 p-8 md:block">
          <h1 class="mt-4 text-3xl font-semibold leading-tight text-white">实时运行状态面板</h1>
          <ul class="mt-6 space-y-3 text-sm text-white/85">
            <li>实时查看节点在线状态</li>
            <li>用户连接与流量统计</li>
            <li>驱动缓存和错误告警快照</li>
          </ul>
        </section>

        <section class="p-6 md:p-10">
          <n-space vertical :size="18">
            <div>
              <h2 class="text-2xl font-semibold text-slate-50">登录面板</h2>
              <p class="mt-2 text-sm text-slate-400">请输入密码访问。</p>
            </div>

            <n-form @submit.prevent="submit">
              <n-form-item label="Token">
                <n-input
                  v-model:value="token"
                  type="password"
                  show-password-on="click"
                  clearable
                  placeholder="请输入密码"
                  @keydown.enter.prevent="submit"
                />
              </n-form-item>
              <n-button
                type="primary"
                :loading="authStore.checking"
                :disabled="token.trim().length === 0"
                block
                @click="submit"
              >
                登录
              </n-button>
            </n-form>
          </n-space>
        </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useMessage } from 'naive-ui'
import { useAuthStore } from '../stores/auth'

const token = ref('')
const authStore = useAuthStore()
const router = useRouter()
const route = useRoute()
const message = useMessage()

async function submit() {
  try {
    await authStore.login(token.value)
    message.success('登录成功')
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    await router.replace(redirect)
  } catch {
    message.error('Token 无效或接口不可用')
  }
}
</script>
