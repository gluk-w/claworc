import { SITE_URL, GOOGLE_SITE_VERIFICATION, BING_SITE_VERIFICATION } from 'astro:env/server';

export interface SiteConfig {
  name: string;
  description: string;
  url: string;
  ogImage: string;
  author: string;
  email: string;
  phone?: string;
  address?: {
    street: string;
    city: string;
    state: string;
    zip: string;
    country: string;
  };
  socialLinks: string[];
  twitter?: {
    site: string;
    creator: string;
  };
  verification?: {
    google?: string;
    bing?: string;
  };
  authorImage?: string;
  blogImageOverlay?: boolean;
  branding: {
    logo: {
      alt: string;
      imageUrl?: string;
    };
    favicon: {
      svg: string;
    };
    colors: {
      themeColor: string;
      backgroundColor: string;
    };
  };
}

const siteConfig: SiteConfig = {
  name: 'Claworc',
  description:
    'OpenClaw Orchestrator — manage fleets of OpenClaw agents in Kubernetes or Docker. A dashboard for Chromium-based browser agents, terminal access, LLM gateway, skills library, backups, and shared folders.',
  url: SITE_URL || 'https://claworc.com',
  ogImage: '/images/dashboard2.png',
  author: 'Claworc',
  email: 'hello@claworc.com',
  socialLinks: ['https://github.com/gluk-w/claworc'],
  verification: {
    google: GOOGLE_SITE_VERIFICATION,
    bing: BING_SITE_VERIFICATION,
  },
  authorImage: '/images/logo.svg',
  blogImageOverlay: false,
  branding: {
    logo: {
      alt: 'Claworc',
      imageUrl: '/images/logo.svg',
    },
    favicon: {
      svg: '/favicon.svg',
    },
    colors: {
      themeColor: '#2563EB',
      backgroundColor: '#0f172a',
    },
  },
};

export default siteConfig;
