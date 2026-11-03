// The docs are written to read on GitHub, so they link to each other as
// sibling files (`object-cache.md#shared-database`) and to the code by repo
// path. On the site those become page URLs. Doing it here rather than writing
// site URLs into the markdown keeps both readings working from one source.

const BASE = '/wp-stage-sync';
const REPO = 'https://github.com/AdaDigitalAgency/wp-stage-sync/blob/main/';

/** Where a docs-relative href points on the site, or undefined to leave it. */
export function siteHref(href) {
  if (/^([a-z]+:|#|\/)/i.test(href)) return undefined;
  const [path, hash = ''] = href.split('#');
  const anchor = hash ? `#${hash}` : '';
  const sibling = /^(?:\.\/)?([a-z0-9-]+)\.md$/i.exec(path);
  if (sibling) return `${BASE}/docs/${sibling[1].toLowerCase()}/${anchor}`;
  // Anything else relative climbs out of docs/ into the repo.
  const repoPath = path.replace(/^(\.\.\/)+/, '');
  return `${REPO}${repoPath}${anchor}`;
}

// Sätteri mdast plugins (Astro 7's markdown processor).

export const docLinks = {
  name: 'wpss-doc-links',
  link(node, ctx) {
    const next = siteHref(node.url);
    if (next) ctx.setProperty(node, 'url', next);
  },
};

// The pages print each doc's title in their own header, from the same heading
// (lib/docs.ts), so the body's copy goes. A factory, so "first" is per
// document rather than per build.
export const dropTitle = () => {
  let dropped = false;
  return {
    name: 'wpss-drop-title',
    heading(node, ctx) {
      if (dropped || node.depth !== 1) return;
      dropped = true;
      ctx.removeNode(node);
    },
  };
};
