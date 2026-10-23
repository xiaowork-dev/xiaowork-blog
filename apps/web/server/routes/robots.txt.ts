// robots.txt (DISCOVER-05): public pages crawlable; admin, account, auth, API and search results are not.
export default defineEventHandler((event) => {
  setHeader(event, 'content-type', 'text/plain; charset=utf-8')
  setHeader(event, 'cache-control', 'public, max-age=3600')
  return [
    'User-agent: *',
    'Allow: /',
    'Disallow: /admin',
    'Disallow: /account',
    'Disallow: /auth',
    'Disallow: /api/',
    'Disallow: /search',
    '',
    `Sitemap: ${siteUrl(event)}/sitemap.xml`,
    '',
  ].join('\n')
})
