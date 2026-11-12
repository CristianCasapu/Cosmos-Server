import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import visualizer from 'rollup-plugin-visualizer';
import { createHash } from 'crypto';
import { readFileSync, readdirSync } from 'fs';

// Fork addition: the skin in client/public/skin is copied to the build as-is,
// so its files get no content hash of their own. Stamp their URLs in
// index.html with one, so browsers refetch them when they change.
const skinVersion = () => {
  const dir = 'client/public/skin';
  const hash = createHash('sha256');
  for (const file of readdirSync(dir).sort()) {
    hash.update(readFileSync(`${dir}/${file}`));
  }
  const version = hash.digest('hex').slice(0, 10);
  return {
    name: 'skin-version',
    transformIndexHtml: (html) => html.replaceAll('__SKIN_VERSION__', version),
  };
};

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), skinVersion()],
  root: 'client',
  build: {
    outDir: '../static',
    rollupOptions: {
      plugins: [visualizer({ open: true })],
    },
  },
  server: {
    proxy: {
      '/cosmos/api': {
        target: 'http://localhost:8080',
        // target: 'http://192.168.1.170:8080',
        secure: false,
        ws: true,
      },
      '/cosmos/rclone': {
        target: 'http://localhost:8080',
        // target: 'http://192.168.1.170:8080',
        secure: false,
        ws: true,
      }
    }
  }
})
