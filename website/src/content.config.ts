import { defineCollection, reference } from 'astro:content';
import { z } from 'astro/zod';
import { glob } from 'astro/loaders';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

// -----------------------------------------------------------------------------
// Shared primitives — reused across collections so every page / section uses
// the same shape. Zod validation is the consistency gate: bad frontmatter
// fails `astro check`.
// -----------------------------------------------------------------------------

const cta = z.object({
  label: z.string(),
  href: z.string(),
  variant: z.enum(['primary', 'secondary', 'ghost']).default('primary'),
  external: z.boolean().default(false),
});

const iconName = z.string(); // key into src/lib/icons.ts

const featureCard = z.object({
  icon: iconName,
  title: z.string(),
  body: z.string(),
  href: z.string().optional(),
});

const iconItem = z.object({
  icon: iconName,
  title: z.string(),
  body: z.string(),
});

const screenshot = z.object({
  src: z.string(), // path under /images/...
  alt: z.string(), // required for a11y
  caption: z.string().optional(),
});

const splitSection = z.object({
  id: z.string().optional(),
  eyebrow: z.string().optional(),
  title: z.string(),
  body: z.string(),
  bullets: z.array(iconItem).default([]),
  media: z.union([
    screenshot,
    z.object({ slider: z.array(screenshot).min(2) }),
  ]),
  mediaSide: z.enum(['left', 'right']).default('right'),
  background: z.enum(['default', 'alt']).default('default'),
});

// -----------------------------------------------------------------------------
// Landing collection — the homepage is data-driven. A single entry named
// `home` with structured frontmatter is the only way to describe the
// landing page, so every section is forced through the shared components.
// -----------------------------------------------------------------------------

const landing = defineCollection({
  loader: glob({ pattern: '**/*.{md,mdx}', base: './src/content/landing' }),
  schema: z.object({
    title: z.string(),
    description: z.string().max(200),
    hero: z.object({
      eyebrow: z.string().optional(),
      heading: z.string(),
      subheading: z.string(),
      body: z.string(),
      ctas: z.array(cta).min(1).max(3),
      screenshot: screenshot,
    }),
    features: z.object({
      heading: z.string(),
      subheading: z.string().optional(),
      items: z.array(featureCard).min(3).max(12),
    }),
    instance: z.object({
      heading: z.string(),
      body: z.string(),
      items: z.array(iconItem).length(5),
      footnote: z.string().optional(),
    }),
    sections: z.array(splitSection),
    install: z.object({
      heading: z.string(),
      subheading: z.string(),
      command: z.string(),
      commandHref: z.string(),
      tiles: z
        .array(
          z.object({
            icon: iconName,
            title: z.string(),
            body: z.string(),
            href: z.string(),
            cta: z.string(),
          })
        )
        .min(3)
        .max(4),
    }),
  }),
});

// -----------------------------------------------------------------------------
// Blog collection — articles for the Claworc blog. Every post must have a
// title, description, publish date, and a valid author reference.
// -----------------------------------------------------------------------------

const blog = defineCollection({
  loader: glob({ pattern: '**/*.{md,mdx}', base: './src/content/blog' }),
  schema: ({ image }) =>
    z
      .object({
        title: z.string().max(120),
        description: z.string().max(200),
        publishedAt: z.coerce.date(),
        updatedAt: z.coerce.date().optional(),
        author: reference('authors'),
        tags: z.array(z.string()).default([]),
        image: image().optional(),
        imageAlt: z.string().optional(),
        draft: z.boolean().default(false),
        featured: z.boolean().default(false),
        canonicalUrl: z.string().url().optional(),
      })
      .refine((d) => !d.image || !!d.imageAlt, {
        message: 'imageAlt is required when image is set',
        path: ['imageAlt'],
      }),
});

// -----------------------------------------------------------------------------
// Authors collection
// -----------------------------------------------------------------------------

const authors = defineCollection({
  loader: glob({ pattern: '**/*.json', base: './src/content/authors' }),
  schema: ({ image }) =>
    z.object({
      name: z.string(),
      bio: z.string(),
      avatar: image().optional(),
      social: z
        .object({
          twitter: z.string().optional(),
          github: z.string().optional(),
          linkedin: z.string().optional(),
        })
        .optional(),
    }),
});

// -----------------------------------------------------------------------------
// Docs collection — uses Starlight's built-in loader and schema so that
// Starlight owns routing under /docs/* via routePrefix in astro.config.
// -----------------------------------------------------------------------------

const docs = defineCollection({
  loader: docsLoader(),
  schema: docsSchema(),
});

export const collections = {
  landing,
  blog,
  authors,
  docs,
};
