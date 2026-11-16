/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { mockApi } from './mock/plugin';

// The app is served from "/" by the Go daemon with history-API fallback, so the
// base stays absolute. Build output goes to web/dist, which embed.go picks up.
export default defineConfig({
  plugins: [react(), mockApi()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
    // Keep the module preload polyfill out: it is injected as part of the entry
    // chunk anyway, but turning it off keeps the output smaller for the Pi.
    modulePreload: { polyfill: false },
  },
  server: {
    port: 5173,
    strictPort: false,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
