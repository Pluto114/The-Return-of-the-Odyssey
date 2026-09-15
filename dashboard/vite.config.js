import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'ODYSSEY_')
  const target = env.ODYSSEY_ADMIN_PROXY_TARGET || 'http://127.0.0.1:8080'
  return {
    plugins: [vue()],
    server: {
      port: 5173,
      strictPort: true,
      proxy: {
        '/api': { target },
        '/ws': { target, ws: true },
      },
    },
    preview: { port: 4173, strictPort: true },
    build: { chunkSizeWarningLimit: 650 },
  }
})
