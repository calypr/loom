import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  resolve: { dedupe: ['react', 'react-dom'] },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'loom-assets',
  },
  server: {
    port: 8080,
    host: '0.0.0.0',
    proxy: {
      '/api': { target: process.env.LOOM_API_URL ?? 'http://127.0.0.1:8080', changeOrigin: true },
      '/graphql': { target: process.env.LOOM_API_URL ?? 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
  optimizeDeps: { include: ['@calypr/loom-ui'] },
});
