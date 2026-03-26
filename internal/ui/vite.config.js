import { defineConfig } from 'vite'
import preactPlugin from '@preact/preset-vite'

export default defineConfig({
  plugins: [preactPlugin()],
  build: {
    outDir: 'dist',
    minify: 'terser',
    sourcemap: false,
    target: 'es2020',
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})


