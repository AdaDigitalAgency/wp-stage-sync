import { getCollection, type CollectionEntry } from 'astro:content';

// Reading order for the docs navigation. A doc missing from this list still
// gets a page, listed after these, so a new file is never silently left out.
const ORDER = [
  'getting-started', 'interactive', 'unattended', 'configuration', 'how-it-works',
  'cache-plugins', 'object-cache', 'servers', 'promote-and-restore', 'safety', 'troubleshooting',
];

export interface DocMeta {
  slug: string;
  title: string;
  description: string;
  entry: CollectionEntry<'docs'>;
}

/** The first `# ` heading. Every doc has one; a doc without one is a mistake worth failing on. */
export function titleOf(entry: CollectionEntry<'docs'>): string {
  const match = /^# (.+)$/m.exec(entry.body ?? '');
  if (!match) throw new Error(`docs/${entry.id}.md has no "# " title`);
  return match[1]!.trim();
}

/**
 * The opening of the first paragraph after the title, as plain text, for meta
 * descriptions, the docs index and llms.txt: whole sentences up to about 160
 * characters.
 */
export function descriptionOf(entry: CollectionEntry<'docs'>): string {
  const body = (entry.body ?? '').replace(/^# .+$/m, '');
  const paragraph = body.split(/\n\s*\n/).map((p) => p.trim()).find((p) => p && !/^(#|```|\||>|- |\d+\. )/.test(p)) ?? '';
  const text = paragraph
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
    .replace(/[`*_]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
  // Split only where a stop is followed by a space, so "wp-config.php" and
  // "example.com" in the middle of a sentence stay put.
  const sentences = text.split(/(?<=[.!?])\s+/);
  let out = '';
  for (const sentence of sentences) {
    if (out && `${out} ${sentence}`.length > 160) break;
    out = out ? `${out} ${sentence}` : sentence;
  }
  return out;
}

export async function allDocs(): Promise<DocMeta[]> {
  const entries = await getCollection('docs');
  const rank = (slug: string) => {
    const i = ORDER.indexOf(slug);
    return i === -1 ? ORDER.length : i;
  };
  return entries
    .map((entry) => ({ slug: entry.id, title: titleOf(entry), description: descriptionOf(entry), entry }))
    .sort((a, b) => rank(a.slug) - rank(b.slug) || a.slug.localeCompare(b.slug));
}
