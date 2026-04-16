/**
 * Navigation Configuration
 *
 * Defines which pages appear in the site navigation and their display order.
 * Astro handles routing via the filesystem — this only controls nav menus.
 */

export interface NavItem {
  label: string;
  href: string;
  order: number;
  external?: boolean;
}

export const navItems: NavItem[] = [
  { label: 'Install', href: '#install', order: 1 },
  { label: 'Features', href: '#features', order: 2 },
  { label: 'Blog', href: '/blog', order: 3 },
  { label: 'Docs', href: '/docs', order: 4 },
  { label: 'GitHub', href: 'https://github.com/gluk-w/claworc', order: 5, external: true },
];

/**
 * Get navigation items sorted by order
 */
export function getNavItems(): NavItem[] {
  return [...navItems].sort((a, b) => a.order - b.order);
}
