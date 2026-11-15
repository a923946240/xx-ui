export const appName = 'xx-ui';
export const appTagline = 'Advanced web panel for managing Xray-core servers';

export const docsRoute = '/docs';
export const docsImageRoute = '/og/docs';
export const docsContentRoute = '/llms.mdx/docs';

// The xx-ui product repository — used for the navbar GitHub link,
// build-time star/release stats, and install commands.
export const productRepo = {
  user: 'a923946240',
  repo: 'xx-ui',
  branch: 'main',
};

// Where these docs live in the xx-ui monorepo — used for "Edit on GitHub" links.
export const gitConfig = {
  user: 'a923946240',
  repo: 'xx-ui',
  branch: 'main',
  docsDir: 'docs/content/docs',
};

export const productRepoUrl = `https://github.com/${productRepo.user}/${productRepo.repo}`;

// Public site origin, used for metadataBase / canonical URLs / OG images.
// Defaults to the production domain, so the env var is optional. Use `||` (not
// `??`) so an empty string — e.g. an unset `${{ vars.NEXT_PUBLIC_SITE_URL }}`
// in CI — also falls back instead of shipping a blank origin.
export const siteUrl = process.env.NEXT_PUBLIC_SITE_URL || productRepoUrl;
