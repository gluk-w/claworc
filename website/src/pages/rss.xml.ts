import { getCollection, getEntry } from 'astro:content';
import type { APIContext } from 'astro';
import siteConfig from '@/config/site.config';

export const prerender = true;

/**
 * Escapes XML special characters
 */
function escapeXml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

/**
 * Formats a date to RFC-822 format for RSS
 */
function formatRfc822Date(date: Date): string {
  return date.toUTCString();
}

export async function GET(context: APIContext) {
  const posts = await getCollection('blog', ({ data }) => !data.draft);

  const sortedPosts = posts.sort(
    (a, b) => new Date(b.data.publishedAt).getTime() - new Date(a.data.publishedAt).getTime()
  );

  const site = context.site?.toString() ?? siteConfig.url;
  const siteUrl = site.endsWith('/') ? site.slice(0, -1) : site;

  const items = (
    await Promise.all(
      sortedPosts.map(async (post) => {
        const link = `${siteUrl}/blog/${post.id}/`;
        const categories = post.data.tags
          .map((tag) => `<category>${escapeXml(tag)}</category>`)
          .join('\n        ');
        const authorEntry = await getEntry(post.data.author);
        const authorName = authorEntry?.data.name ?? siteConfig.name;

        return `    <item>
      <title>${escapeXml(post.data.title)}</title>
      <link>${link}</link>
      <guid>${link}</guid>
      <description>${escapeXml(post.data.description)}</description>
      <pubDate>${formatRfc822Date(post.data.publishedAt)}</pubDate>
      <author>${escapeXml(authorName)}</author>
      ${categories}
    </item>`;
      })
    )
  ).join('\n');

  const rss = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
  <channel>
    <title>${escapeXml(siteConfig.name)}</title>
    <description>${escapeXml(siteConfig.description)}</description>
    <link>${siteUrl}</link>
    <atom:link href="${siteUrl}/rss.xml" rel="self" type="application/rss+xml"/>
    <language>en-us</language>
    <lastBuildDate>${formatRfc822Date(new Date())}</lastBuildDate>
${items}
  </channel>
</rss>`;

  return new Response(rss, {
    headers: {
      'Content-Type': 'application/xml; charset=utf-8',
    },
  });
}
