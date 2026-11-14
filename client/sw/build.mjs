// Fork addition: puts the panel's service worker together when the UI is
// built (hooked into vite.config.js): the worker's source (worker.js), the
// rules it follows (../src/utils/pwaPolicy.js, as text) and the list of the
// build's own files, which is everything the worker may ever keep.
//
//   core   what index.html asks for when the panel starts, the offline page
//          and what it asks for, and the skin's font: fetched when the
//          worker is installed
//   later  the build's other files (translations, pictures): kept when a
//          page first asks for one
// Not in either list, so never kept: index.html itself, the manifest, the
// worker, the pictures for the install dialog.
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));

export const BASE = '/cosmos-ui/';
const KEEPABLE = /\.(js|css|woff2|png|jpg|svg|html)$/i;
const NEVER = [/^index\.html$/, /^sw\.js$/, /^manifest\.webmanifest$/, /^pwa\/screenshot-/, /^stats\.html$/];
const ALWAYS_CORE = ['offline.html', 'skin/inter.woff2'];

// The addresses below the base that a page's HTML asks for (src and href
// attributes), each as "path?query" exactly as written.
export const referencedBy = (html, base = BASE) => {
  const found = [];
  const pattern = /\b(?:src|href)\s*=\s*"([^"]+)"/g;
  for (let m = pattern.exec(html); m; m = pattern.exec(html)) {
    const value = m[1].split('#')[0];
    if (value.startsWith(base) && !found.includes(value)) found.push(value);
  }
  return found;
};

// pages: the HTML of index.html and of offline.html (a string for one page);
// files: the build's files, relative to its folder ("assets/index-1a2b.js").
export const buildLists = (pages, files, base = BASE) => {
  const allowed = (file) => KEEPABLE.test(file) && !NEVER.some((re) => re.test(file));
  const present = new Set(files);
  const core = [];
  for (const key of [].concat(pages).flatMap((html) => referencedBy(html, base))) {
    const file = key.slice(base.length).split('?')[0];
    if (present.has(file) && allowed(file) && !core.includes(key)) core.push(key);
  }
  for (const file of ALWAYS_CORE) {
    if (!present.has(file)) throw new Error('the build lacks ' + file + ', which the service worker needs');
    if (!core.includes(base + file)) core.push(base + file);
  }
  const coreFiles = new Set(core.map((key) => key.slice(base.length).split('?')[0]));
  const later = files.filter((file) => allowed(file) && !coreFiles.has(file)).map((file) => base + file).sort();
  return { core: core.sort(), later };
};

// The build's name: the fork's build number when a release is built, and a
// digest of everything the worker is made of, so that any change gives a new
// worker and a new cache.
export const versionOf = (forkBuild, lists, sources) => {
  const hash = createHash('sha256');
  hash.update(JSON.stringify(lists));
  for (const source of sources) hash.update(source);
  const digest = hash.digest('hex').slice(0, 12);
  const build = String(forkBuild || '').replace(/[^0-9A-Za-z.-]/g, '').slice(0, 20);
  return (build ? 'fork.' + build + '-' : '') + digest;
};

export const readSources = () => ({
  template: readFileSync(join(here, 'worker.js'), 'utf8'),
  policy: readFileSync(join(here, '../src/utils/pwaPolicy.js'), 'utf8'),
});

// The worker as one classic script (Firefox does not load a worker that is a module).
export const assembleWorker = ({ template, policy }, lists, version) => {
  const rules = policy.replace(/^export\s+/gm, '');
  if (/^\s*import\s/m.test(rules)) throw new Error('pwaPolicy.js must not import anything: its text is put into the worker');
  const put = (text, mark, value) => {
    if (!text.includes(mark)) throw new Error('worker.js lacks the mark ' + mark);
    return text.replace(mark, () => value);
  };
  let out = put(template, '/* __POLICY__ */', rules);
  out = put(out, "'__VERSION__'", JSON.stringify(version));
  out = put(out, '/* __CORE__ */[]', JSON.stringify(lists.core));
  out = put(out, '/* __LATER__ */[]', JSON.stringify(lists.later));
  return out;
};

const walk = (dir, prefix = '') => readdirSync(dir).flatMap((name) => {
  const path = join(dir, name);
  return statSync(path).isDirectory() ? walk(path, prefix + name + '/') : [prefix + name];
});

// The Vite plugin: once the build's files are written, write sw.js next to them.
export const pwaWorker = () => {
  let outDir = null;
  return {
    name: 'fork-pwa-worker',
    apply: 'build',
    configResolved(config) { outDir = join(config.root, config.build.outDir); },
    closeBundle() {
      const files = walk(outDir).sort();
      const page = (name) => (files.includes(name) ? readFileSync(join(outDir, name), 'utf8') : '');
      const lists = buildLists([page('index.html'), page('offline.html')], files);
      const sources = readSources();
      const version = versionOf(process.env.VITE_FORK_BUILD, lists, [sources.template, sources.policy]);
      writeFileSync(join(outDir, 'sw.js'), assembleWorker(sources, lists, version));
      console.log(`service worker ${version}: ${lists.core.length} files kept at once, ${lists.later.length} when first used`);
    },
  };
};
