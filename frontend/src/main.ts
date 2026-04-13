import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import router from './router'
import { registerRouter } from './router/bridge'
import App from './App.vue'
import './style.css'
import '@/assets/styles/variables.css'
import '@/assets/styles/common.css'

const app = createApp(App)
const pinia = createPinia()

registerRouter(router)

app.use(pinia)
app.use(router)
app.use(ElementPlus)

app.mount('#app')
