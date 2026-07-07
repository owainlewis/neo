// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  // TODO: set to the real docs domain once Cloudflare Pages is connected.
  site: 'https://docs.neo.dev',
  integrations: [
    starlight({
      title: 'Neo',
      description: 'A terminal-first coding agent, written in Go.',
      favicon: '/favicon.svg',
      social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/owainlewis/neo' }],
      editLink: {
        baseUrl: 'https://github.com/owainlewis/neo/edit/main/website/',
      },
      customCss: ['./src/styles/custom.css'],
      pagination: false,
      sidebar: [
        {
          label: 'Start here',
          items: [
            { label: 'Overview', slug: 'docs/reference' },
            { label: 'Install', slug: 'docs/install' },
            { label: 'Quick start', slug: 'docs/quick-start' },
          ],
        },
        {
          label: 'Guides',
          items: [
            { label: 'Overview', slug: 'docs/reference/guides' },
            { label: 'Agent loop', slug: 'docs/reference/guides/agent-loop' },
            { label: 'System prompt', slug: 'docs/reference/guides/system-prompt' },
            { label: 'Tools', slug: 'docs/reference/guides/tools' },
            { label: 'Permissions', slug: 'docs/reference/guides/permissions' },
            { label: 'Providers', slug: 'docs/reference/guides/providers' },
            { label: 'Sessions', slug: 'docs/reference/guides/sessions' },
            { label: 'Compaction', slug: 'docs/reference/guides/compaction' },
            { label: 'Memory', slug: 'docs/reference/guides/memory' },
          ],
        },
        {
          label: 'Reference',
          items: [
            { label: 'Architecture', slug: 'docs/reference/architecture' },
            { label: 'CLI', slug: 'docs/reference/cli' },
            { label: 'Configuration', slug: 'docs/reference/config' },
            { label: 'Sessions', slug: 'docs/reference/sessions' },
            { label: 'Tools', slug: 'docs/reference/tools' },
            { label: 'Prompt caching', slug: 'docs/reference/prompt-caching' },
          ],
        },
      ],
    }),
  ],
});
