import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
    globals: true,
    // 既有 frontend/tests/*.test.ts 使用 node:test 风格（import test from 'node:test'），
    // 与 vitest 运行器不兼容。仅收集 src 下的 vitest 测试，避免误运行 node:test 文件。
    include: ['src/**/*.spec.ts', 'src/**/*.test.ts'],
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
})
