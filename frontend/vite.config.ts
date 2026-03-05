import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'
import { readFileSync } from 'fs'

// 读取根目录 VERSION 文件
let appVersion = 'dev'
try {
  const versionPath = resolve(__dirname, '..', 'VERSION')
  appVersion = readFileSync(versionPath, 'utf-8').trim()
} catch (e) {
  console.warn('VERSION file not found, using dev')
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
  },
})
