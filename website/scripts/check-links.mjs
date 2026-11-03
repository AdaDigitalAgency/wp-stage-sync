// Fails the build when an internal link in dist/ points at a missing page or
// a missing #anchor. The docs link to each other as sibling .md files with
// GitHub-style anchors, so a renamed heading would otherwise break silently.
import { readdirSync, readFileSync, existsSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

const DIST = new URL('../dist/', import.meta.url).pathname;
const BASE = '/wp-stage-sync/';
const REPO = 'https://github.com/AdaDigitalAgency/wp-stage-sync/blob/main/';
const ROOT = new URL('../../', import.meta.url).pathname;

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    return statSync(p).isDirectory() ? files(p) : [p];
  });
}

const pages = files(DIST).filter((f) => f.endsWith('.html'));
const ids = new Map();
const idsOf = (file) => {
  if (!ids.has(file)) {
    const html = readFileSync(file, 'utf8');
    ids.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
  }
  return ids.get(file);
};

// The target file of a site path: /wp-stage-sync/docs/x/ → dist/docs/x/index.html.
function target(path) {
  let rel = decodeURIComponent(path.slice(BASE.length));
  if (rel === '' || rel.endsWith('/')) rel += 'index.html';
  const file = join(DIST, rel);
  if (existsSync(file) && statSync(file).isFile()) return file;
  if (existsSync(join(file, 'index.html'))) return join(file, 'index.html');
  return undefined;
}

const problems = [];
for (const page of pages) {
  const html = readFileSync(page, 'utf8');
  for (const [, href] of html.matchAll(/\shref="([^"]+)"/g)) {
    const [path, anchor] = href.split('#');
    let file;
    if (path === '') file = page;
    else if (path.startsWith(BASE)) file = target(path);
    else if (path.startsWith(REPO)) {
      // A link into the code: the file must exist in this checkout.
      const repoPath = decodeURIComponent(path.slice(REPO.length));
      if (!existsSync(join(ROOT, repoPath))) problems.push(`${relative(DIST, page)}: ${href} (no ${repoPath} in the repo)`);
      continue;
    } else continue; // external or another site on the host
    const where = relative(DIST, page);
    if (!file) { problems.push(`${where}: ${href} (no such page)`); continue; }
    if (anchor && file.endsWith('.html') && !idsOf(file).has(anchor)) {
      problems.push(`${where}: ${href} (no #${anchor} on that page)`);
    }
  }
}

if (problems.length) {
  console.error(`Broken internal links:\n  ${[...new Set(problems)].join('\n  ')}`);
  process.exit(1);
}
console.log(`Links OK: ${pages.length} pages checked.`);
