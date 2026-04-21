import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'
import { readFileSync } from 'fs'

let appVersion = 'dev'
try {
  const versionPath = resolve(__dirname, '..', 'VERSION')
  appVersion = readFileSync(versionPath, 'utf-8').trim()
} catch (e) {
  console.warn('VERSION file not found, using dev')
}

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) {
            return undefined
          }
          if (id.includes('element-plus') || id.includes('@element-plus')) {
            return 'element-plus'
          }
          if (id.includes('vue')) {
            return 'vue-vendor'
          }
          if (id.includes('axios') || id.includes('dayjs') || id.includes('uuid')) {
            return 'app-vendor'
          }
          return 'vendor'
        },
      },
    },
  },
})
