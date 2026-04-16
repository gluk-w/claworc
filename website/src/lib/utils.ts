/**
 * Format a date for display
 */
export function formatDate(date: Date, locale = 'en-US'): string {
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  }).format(date);
}

/**
 * Calculate reading time for content
 */
export function getReadingTime(content: string): number {
  const wordsPerMinute = 200;
  const words = content.trim().split(/\s+/).length;
  return Math.ceil(words / wordsPerMinute);
}

/**
 * Generate a unique ID
 */
export function generateId(prefix = 'id'): string {
  return `${prefix}-${Math.random().toString(36).slice(2, 9)}`;
}

/**
 * Check if a URL is external
 */
export function isExternalUrl(url: string): boolean {
  return url.startsWith('http://') || url.startsWith('https://');
}

/**
 * Resolve a flat array of social profile URLs into structured link objects.
 * Matches each URL against known platforms to derive icon name and label.
 */
const SOCIAL_PLATFORMS = [
  { key: 'github',    match: /github\.com/i,                  label: 'GitHub',      icon: 'github'    },
  { key: 'twitter',   match: /x\.com|twitter\.com/i,          label: 'X / Twitter', icon: 'x-twitter' },
  { key: 'linkedin',  match: /linkedin\.com/i,                label: 'LinkedIn',    icon: 'linkedin'  },
  { key: 'instagram', match: /instagram\.com/i,               label: 'Instagram',   icon: 'instagram' },
  { key: 'bluesky',   match: /bsky\.app|bluesky\.social/i,    label: 'Bluesky',     icon: 'bluesky'   },
] as const;

export interface ResolvedSocialLink {
  key: string;
  href: string;
  label: string;
  icon: string;
}

export function resolveSocialLinks(urls: string[]): ResolvedSocialLink[] {
  return urls.flatMap((href) => {
    const platform = SOCIAL_PLATFORMS.find((p) => p.match.test(href));
    if (!platform) return [];
    return [{ key: platform.key, href, label: platform.label, icon: platform.icon }];
  });
}
