/// <reference types="vitest/config" />
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// 开发时后端由 `easel serve` 提供（`make dev` 会同时启动），这里把 API 请求转发过去。
const backend = process.env.EASEL_DEV_BACKEND ?? 'http://127.0.0.1:7780'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  test: {
    environment: 'jsdom',
  },
  server: {
    host: '127.0.0.1',
    proxy: {
      '/api': backend,
      '/healthz': backend,
    },
  },
})
