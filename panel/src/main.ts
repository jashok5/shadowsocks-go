import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { useAuthStore } from './stores/auth'
import './style.css'

const app = createApp(App)
const pinia = createPinia()

const authBoot = useAuthStore(pinia)
authBoot.hydrate()

router.beforeEach((to) => {
  const auth = useAuthStore(pinia)
  auth.hydrate()
  if (to.meta.requiresAuth && !auth.isLoggedIn) {
    auth.logout()
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && auth.isLoggedIn) {
    const target = typeof to.query.redirect === 'string' ? to.query.redirect : '/'
    return target
  }
  return true
})

app.use(pinia)
app.use(router)
app.mount('#app')
