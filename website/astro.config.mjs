// @ts-check
import { defineConfig } from 'astro/config';
import { satteri } from '@astrojs/markdown-satteri';
import { docLinks, dropTitle } from './src/lib/markdown.mjs';

export default defineConfig({
  site: 'https://ada.tools',
  base: '/wp-stage-sync',
  // Directory output, so /wp-stage-sync/docs/safety is answered by
  // docs/safety/index.html. LiteSpeed on ada.tools redirects the bare path to
  // the slashed one, which serves an extensionless URL without server config.
  build: { format: 'directory' },
  trailingSlash: 'ignore',
  // The landing page was hand-written HTML whose line breaks between text and
  // links are the spaces ("Download it and read"). Compression drops them.
  compressHTML: false,
  markdown: {
    // The docs are shell, PHP and JSON snippets. Plain monospace blocks need no
    // second colour palette for the dark theme.
    syntaxHighlight: false,
    processor: satteri({ mdastPlugins: [docLinks, dropTitle] }),
  },
  vite: {
    // The docs collection reads ../docs.
    server: { fs: { allow: ['..'] } },
  },
});
