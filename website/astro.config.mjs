import { defineConfig, envField } from 'astro/config';
import mdx from '@astrojs/mdx';
import sitemap from '@astrojs/sitemap';
import react from '@astrojs/react';
import icon from 'astro-icon';
import starlight from '@astrojs/starlight';
import tailwindcss from '@tailwindcss/vite';

// Static site deployed to Cloudflare Workers via wrangler.toml [site] bucket.
// No SSR adapter — everything under ./dist is served as static assets.

export default defineConfig({
  site: process.env.SITE_URL || 'https://claworc.com',
  output: 'static',
  trailingSlash: 'ignore',

  env: {
    schema: {
      SITE_URL: envField.string({ context: 'server', access: 'public', optional: true }),
      PUBLIC_GA_MEASUREMENT_ID: envField.string({ context: 'client', access: 'public', optional: true, default: 'G-6D2TB85Z1J' }),
      PUBLIC_GTM_ID: envField.string({ context: 'client', access: 'public', optional: true }),
      GOOGLE_SITE_VERIFICATION: envField.string({ context: 'server', access: 'public', optional: true }),
      BING_SITE_VERIFICATION: envField.string({ context: 'server', access: 'public', optional: true }),
      PUBLIC_CONSENT_ENABLED: envField.boolean({ context: 'client', access: 'public', optional: true, default: false }),
      PUBLIC_PRIVACY_POLICY_URL: envField.string({ context: 'client', access: 'public', optional: true, default: '' }),
    },
  },

  image: {
    layout: 'constrained',
  },

  integrations: [
    react({ include: ['**/*.{jsx,tsx}'] }),
    starlight({
      title: 'Claworc Docs',
      disable404Route: true,
      logo: {
        src: './public/images/logo.svg',
        replacesTitle: true,
      },
      favicon: '/favicon.svg',
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/gluk-w/claworc' },
      ],
      editLink: {
        baseUrl: 'https://github.com/gluk-w/claworc/edit/main/website/src/content/docs/',
      },
      components: {
        Header: './src/components/docs/Header.astro',
        Footer: './src/components/docs/Footer.astro',
        PageFrame: './src/components/docs/PageFrame.astro',
      },
      customCss: ['./src/styles/global.css', './src/styles/starlight.css'],
      head: [
        {
          tag: 'script',
          attrs: { async: true, src: 'https://www.googletagmanager.com/gtag/js?id=G-6D2TB85Z1J' },
        },
        {
          tag: 'script',
          content:
            "window.dataLayer = window.dataLayer || []; function gtag(){dataLayer.push(arguments);} gtag('js', new Date()); gtag('config', 'G-6D2TB85Z1J');",
        },
      ],
      sidebar: [
        {
          label: 'Getting started',
          items: [
            { label: 'Introduction', slug: 'docs' },
            { label: 'Quickstart', slug: 'docs/quickstart' },
            { label: 'Installation', slug: 'docs/installation' },
          ],
        },
        {
          label: 'Instances',
          items: [
            { label: 'Instances', slug: 'docs/instances' },
            { label: 'Accessing', slug: 'docs/accessing' },
            { label: 'Skills', slug: 'docs/skills' },
            { label: 'Backups', slug: 'docs/backups' },
            { label: 'Shared folders', slug: 'docs/shared-folders' },
          ],
        },
        {
          label: 'Models',
          items: [
            { label: 'Overview', slug: 'docs/models/overview' },
            { label: 'Configuration', slug: 'docs/models/configuration' },
            { label: 'Assign models', slug: 'docs/models/assign-models' },
            { label: 'Usage dashboard', slug: 'docs/models/usage-dashboard' },
          ],
        },
        {
          label: 'Security & access',
          items: [
            { label: 'Authentication', slug: 'docs/authentication' },
            { label: 'SSH', slug: 'docs/ssh' },
            { label: 'Environment variables', slug: 'docs/environment-variables' },
          ],
        },
        {
          label: 'AI tools',
          items: [
            { label: 'Claude Code', slug: 'docs/ai-tools/claude-code' },
            { label: 'Cursor', slug: 'docs/ai-tools/cursor' },
            { label: 'Windsurf', slug: 'docs/ai-tools/windsurf' },
          ],
        },
      ],
    }),
    mdx(),
    sitemap(),
    icon(),
  ],

  vite: {
    plugins: [tailwindcss()],
  },

  security: {
    checkOrigin: true,
  },

  markdown: {
    shikiConfig: {
      theme: 'github-dark',
      wrap: true,
    },
  },
});
