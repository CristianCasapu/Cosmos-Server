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

// Dev API target. Default is the local backend; point it at a live server to
// debug the UI against real data (unminified, with sourcemaps):
//
//   COSMOS_API=https://cluster.cosmos-cloud.io npm run client
//
// then open http://localhost:5173/cosmos-ui/ and log in as usual. The remote
// sets its cookies for its own domain with Secure, which a browser on
// localhost would drop, so both attributes are stripped on the way back.
const apiTarget = process.env.COSMOS_API || 'http://localhost:8080';
const remote = !/^https?:\/\/(localhost|127\.0\.0\.1)(:|$)/.test(apiTarget);

const proxy = {
  target: apiTarget,
  secure: false,
  ws: true,
  // the server validates the Host header against its hostnames
  changeOrigin: remote,
  cookieDomainRewrite: remote ? '' : undefined,
  configure: remote ? (p) => {
    p.on('proxyRes', (res) => {
      if (res.headers['set-cookie']) {
        res.headers['set-cookie'] = res.headers['set-cookie'].map((c) => c.replace(/;\s*secure/ig, ''));
      }
    });
  } : undefined,
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
      '/cosmos/api': proxy,
      '/cosmos/rclone': proxy,
    }
  }
})
