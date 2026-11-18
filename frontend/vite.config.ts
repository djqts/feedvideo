import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  server: {
    proxy: {
      // Auth endpoints live at the gateway root (no /api prefix). The front-end
      // uses `/v1/auth/*` (baseURL /api → /api/v1/auth/*). More specific keys
      // MUST precede the '/api' catch-all.
      '/api/v1/auth/login': { target: 'http://localhost:8080', rewrite: () => '/login' },
      '/api/v1/auth/register': { target: 'http://localhost:8080', rewrite: () => '/register' },
      '/api/v1/auth/refresh': { target: 'http://localhost:8080', rewrite: () => '/refresh-token' },
      '/api/auth/login': { target: 'http://localhost:8080', rewrite: () => '/login' },
      '/api/auth/register': { target: 'http://localhost:8080', rewrite: () => '/register' },
      '/api/auth/refresh': { target: 'http://localhost:8080', rewrite: () => '/refresh-token' },
      // Existing user routes are registered at the gateway root.
      '^/api/(logout|profile|follow|unfollow|black|unblack|followers|following|friends|blacklist|common-following|is-following|search/users)([?]|$)': {
        target: 'http://localhost:8080',
        rewrite: (requestPath: string) => requestPath.replace(/^\/api/, ''),
      },
      // Business API — forwarded unchanged; backend serves /api/v1/* (no rewrite).
      '/api': 'http://localhost:8080',
      '/storage': {
        target: 'http://localhost:9000',
        rewrite: (path: string) => path.replace(/^\/storage/, ''),
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json', 'html'],
      reportsDirectory: './coverage',
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/test/**', 'src/**/*.test.{ts,tsx}', 'src/**/*.d.ts', 'src/main.tsx'],
    },
  },
})
