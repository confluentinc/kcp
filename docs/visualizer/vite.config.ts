import { readFileSync } from 'node:fs';
import { defineConfig, type Plugin } from 'vite';

// Anime.js is MIT-licensed, and its notice must ship with the copy bundled into
// the page. Minifying strips comments, so the notice goes on afterwards.
const animeLicence = readFileSync(new URL('./node_modules/animejs/LICENSE.md', import.meta.url), 'utf8').trim();

function licenceNotice(): Plugin {
  const banner = `/*! Includes Anime.js (https://animejs.com).\n\n${animeLicence.replaceAll('*/', '* /')}\n*/\n`;
  return {
    name: 'licence-notice',
    apply: 'build',
    generateBundle(_, bundle) {
      for (const file of Object.values(bundle)) {
        if (file.type === 'chunk') file.code = banner + file.code;
      }
    },
  };
}

// Builds into the MkDocs docs_dir, so `mkdocs build` publishes it as static
// files and mike versions it with the rest of the site.
export default defineConfig({
  base: './',
  plugins: [licenceNotice()],
  build: {
    outDir: '../assets/visualizer',
    emptyOutDir: true,
  },
});
