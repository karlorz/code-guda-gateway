import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

/**
 * Admin SPA Vite config.
 *
 * Production embed: `bun run build` → copied into internal/adminweb/assets/dist.
 * Local HMR: `./scripts/dev-up.sh --ui` (or `bun run dev`) serves the SPA on
 * http://127.0.0.1:5173/admin/ and proxies /admin/api/* to the Go gateway so
 * session cookies (Path=/admin) and CSRF stay on the Vite origin.
 *
 * Prefer the Vite URL for UI work. :8080/admin is the last embedded build only.
 */
export default defineConfig(({ mode }) => {
  // loadEnv reads web/admin/.env*; process.env still wins for GUDA_DEV_API from dev-up.
  const env = loadEnv(mode, process.cwd(), '');
  const apiTarget = process.env.GUDA_DEV_API || env.GUDA_DEV_API || 'http://127.0.0.1:8080';

  return {
    base: '/admin/',
    plugins: [react(), tailwindcss()],
    server: {
      host: '127.0.0.1',
      port: 5173,
      strictPort: true,
      // Keep HMR on the same host/port the browser uses (no reverse-proxy dance).
      hmr: {
        host: '127.0.0.1',
        port: 5173,
        clientPort: 5173,
      },
      proxy: {
        // Login, session, CSRF-mutating APIs — everything under /admin/api.
        '/admin/api': {
          target: apiTarget,
          changeOrigin: true,
          secure: false,
        },
      },
    },
    preview: {
      host: '127.0.0.1',
      port: 4173,
      strictPort: true,
      proxy: {
        '/admin/api': {
          target: apiTarget,
          changeOrigin: true,
          secure: false,
        },
      },
    },
    test: {
      environment: 'jsdom',
      exclude: ['**/node_modules/**', '**/dist/**', 'e2e/**'],
      setupFiles: './src/test/setup.ts',
    },
  };
});
