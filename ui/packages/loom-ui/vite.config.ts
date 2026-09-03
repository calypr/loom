import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const directory = dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      input: {
        'loom-ui': resolve(directory, 'src/index.ts'),
        viewer: resolve(directory, 'src/viewer-entry.ts'),
        builder: resolve(directory, 'src/builder-entry.ts'),
      },
      external: ['react', 'react-dom', 'react/jsx-runtime'],
      output: {
        entryFileNames: '[name].js',
        exports: 'named',
        globals: { react: 'React', 'react-dom': 'ReactDOM' },
        assetFileNames: 'styles.css',
      },
      preserveEntrySignatures: 'strict',
    },
  },
});
