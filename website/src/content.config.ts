import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';

// The docs are the repo's own docs/*.md, read in place, so the site and the
// repo cannot drift apart. They carry no frontmatter: the title is the first
// heading, which keeps them plain markdown that reads well on GitHub.
const docs = defineCollection({
  loader: glob({ pattern: '*.md', base: '../docs' }),
});

export const collections = { docs };
