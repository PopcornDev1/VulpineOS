import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/ws': { target: 'ws://localhost:8443', ws: true },
      '/health': 'http://localhost:8443',
      '/api': 'http://localhost:8443',
    }
  }
})
