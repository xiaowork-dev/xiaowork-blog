// RSS 2.0 feed of the 20 newest public articles (title, link, summary, categories). Summaries only:
// the full text stays on the canonical page.
interface Site {
  site_title: string
  site_description: string
  profile: { bio: string }
}

export default defineEventHandler(async (event) => {
  const base = siteUrl(event)
  const [site, page] = await Promise.all([
    publicApi<{ data: Site }>(event, '/api/v1/site').then(r => r.data),
    publicApi<{ data: FeedArticle[] }>(event, '/api/v1/articles', { page: 1, page_size: 20 }),
  ])
  const items = page.data.map((a) => {
    const link = absolute(base, a.canonical_path)
    const categories = [a.category?.name, ...a.tags.map(t => t.name)].filter(Boolean) as string[]
    return [
      '    <item>',
      `      <title>${xml(a.title)}</title>`,
      `      <link>${xml(link)}</link>`,
      `      <guid isPermaLink="true">${xml(link)}</guid>`,
      `      <pubDate>${new Date(a.first_published_at).toUTCString()}</pubDate>`,
      a.summary ? `      <description>${xml(a.summary)}</description>` : '',
      ...categories.map(c => `      <category>${xml(c)}</category>`),
      '    </item>',
    ].filter(Boolean).join('\n')
  })
  const description = site.site_description || site.profile.bio || site.site_title
  const lastBuild = page.data[0] ? new Date(page.data[0].public_updated_at).toUTCString() : new Date(0).toUTCString()

  setHeader(event, 'content-type', 'application/rss+xml; charset=utf-8')
  setHeader(event, 'cache-control', 'public, no-cache')
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">',
    '  <channel>',
    `    <title>${xml(site.site_title)}</title>`,
    `    <link>${xml(base + '/')}</link>`,
    `    <description>${xml(description)}</description>`,
    '    <language>zh-CN</language>',
    `    <lastBuildDate>${lastBuild}</lastBuildDate>`,
    `    <atom:link href="${xml(base + '/rss.xml')}" rel="self" type="application/rss+xml"/>`,
    ...items,
    '  </channel>',
    '</rss>',
    '',
  ].join('\n')
})
