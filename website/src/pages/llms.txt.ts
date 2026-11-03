import type { APIRoute } from 'astro';
import { allDocs } from '../lib/docs';

// The llms.txt index (llmstxt.org): what wp-stage-sync is, and where the docs
// are. Served under /wp-stage-sync and linked from every page head.
export const GET: APIRoute = async ({ site }) => {
  const base = new URL('/wp-stage-sync', site).href;
  const docs = await allDocs();
  const lines = [
    '# wp-stage-sync',
    '',
    '> A Go CLI and TUI that syncs a live WordPress site to its staging site on the same Linux server: database copy with WooCommerce order filtering and anonymisation, wp-content rsync, WP-CLI URL replacement, and cache and CDN cleanup on staging. It never writes to the live database. Promote mode copies selected themes and plugins from staging to live after a backup.',
    '',
    `Install: curl -sSL ${base}/install.sh | bash`,
    '',
    '## Docs',
    '',
    ...docs.map((d) => `- [${d.title}](${base}/docs/${d.slug}/): ${d.description}`),
    '',
  ];
  return new Response(lines.join('\n'), { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
};
