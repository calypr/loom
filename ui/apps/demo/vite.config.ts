import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ command }) => {
  const development = command === 'serve';
  const packageSource = resolve(
    dirname(fileURLToPath(import.meta.url)),
    '../../packages/loom-ui/src',
  );

  return {
    plugins: [react()],
    resolve: {
      dedupe: ['react', 'react-dom'],
      ...(development
        ? {
            alias: {
              '@calypr/loom-ui/builder': resolve(
                packageSource,
                'builder-entry.ts',
              ),
              '@calypr/loom-ui/viewer': resolve(
                packageSource,
                'viewer-entry.ts',
              ),
              '@calypr/loom-ui/styles.css': resolve(
                packageSource,
                'styles.css',
              ),
              '@calypr/loom-ui': resolve(packageSource, 'index.ts'),
            },
          }
        : {}),
    },
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      assetsDir: 'loom-assets',
    },
    server: {
      port: 8080,
      host: '0.0.0.0',
      watch: development ? { usePolling: true, interval: 250 } : undefined,
      proxy: {
        '/api': {
          target: process.env.LOOM_API_URL ?? 'http://127.0.0.1:8080',
          changeOrigin: true,
        },
        '/graphql': {
          target: process.env.LOOM_API_URL ?? 'http://127.0.0.1:8080',
          changeOrigin: true,
        },
      },
    },
    optimizeDeps: development
      ? { exclude: ['@calypr/loom-ui'] }
      : { include: ['@calypr/loom-ui'] },
  };
});
