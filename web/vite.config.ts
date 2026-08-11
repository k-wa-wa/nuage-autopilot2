import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import path from 'path';

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
    },
  },
  build: {
    // assets 直下ではなく 1 段深い dist に出す。
    // 追跡対象の placeholder.html を emptyOutDir に巻き込ませないためである。
    outDir: process.env.OUT_DIR || '../internal/web/assets/dist',
    emptyOutDir: true,
  },
});
