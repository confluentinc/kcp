import path from 'path'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import checker from 'vite-plugin-checker'
import eslint from 'vite-plugin-eslint2'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    eslint({
      lintOnStart: true,
      cache: false,
    }),
    checker({
      typescript: true,
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:5556',
        changeOrigin: true,
      },
      '/health': {
        target: 'http://localhost:5556',
        changeOrigin: true,
      },
      '/upload-state': {
        target: 'http://localhost:5556',
        changeOrigin: true,
      },
      '/metrics': {
        target: 'http://localhost:5556',
        changeOrigin: true,
      },
      '/costs': {
        target: 'http://localhost:5556',
        changeOrigin: true,
      },
      '/assets': {
        target: 'http://localhost:5556',
        changeOrigin: true,
      },
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          // React and React-related libraries
          if (id.includes('node_modules/react/') || id.includes('node_modules/react-dom/')) {
            return 'react-vendor'
          }

          // Large charting library
          if (id.includes('node_modules/recharts')) {
            return 'recharts'
          }

          // JSON Schema Form libraries
          if (id.includes('node_modules/@rjsf/')) {
            return 'rjsf'
          }

          // State management
          if (
            id.includes('node_modules/xstate') ||
            id.includes('node_modules/@xstate/') ||
            id.includes('node_modules/zustand')
          ) {
            return 'state-management'
          }

          // Radix UI components
          if (id.includes('node_modules/@radix-ui/')) {
            return 'radix-ui'
          }

          // Date utilities
          if (
            id.includes('node_modules/date-fns') ||
            id.includes('node_modules/react-day-picker')
          ) {
            return 'date-utils'
          }

          // Lucide icons (depends on React, keep separate)
          if (id.includes('node_modules/lucide-react')) {
            return 'lucide-icons'
          }

          // Other vendor utilities
          if (
            id.includes('node_modules/jszip') ||
            id.includes('node_modules/clsx') ||
            id.includes('node_modules/tailwind-merge') ||
            id.includes('node_modules/class-variance-authority')
          ) {
            return 'utils'
          }
        },
      },
    },
  },
})
