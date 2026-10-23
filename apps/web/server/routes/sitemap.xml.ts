// sitemap.xml (DISCOVER-05/06): home, listing pages, every public article, and categories/tags that
// currently have public articles. Built per request from the public API, so publish/withdraw/delete
// show up on the next fetch (Cache-Control: no-cache → always revalidated).
interface Term {
  slug: string
  article_count: number
}

export default defineEventHandler(async (event) => {
  const base = siteUrl(event)
  const [articles, categories, tags] = await Promise.all([
    allArticles(event),
    publicApi<{ data: Term[] }>(event, '/api/v1/categories').then(r => r.data),
    publicApi<{ data: Term[] }>(event, '/api/v1/tags').then(r => r.data),
  ])
  let about = false
  try {
    await publicApi(event, '/api/v1/pages/about')
    about = true
  }
  catch {
    // no about page yet: leave it out of the sitemap
  }

  const urls: { loc: string, lastmod?: string }[] = [
    { loc: absolute(base, '/'), lastmod: articles[0]?.public_updated_at },
    { loc: absolute(base, '/categories') },
    { loc: absolute(base, '/tags') },
    { loc: absolute(base, '/archives') },
  ]
  if (about) urls.push({ loc: absolute(base, '/about') })
  for (const a of articles) urls.push({ loc: absolute(base, a.canonical_path), lastmod: a.public_updated_at })
  for (const c of categories) if (c.article_count > 0) urls.push({ loc: absolute(base, `/categories/${c.slug}`) })
  for (const t of tags) if (t.article_count > 0) urls.push({ loc: absolute(base, `/tags/${t.slug}`) })

  setHeader(event, 'content-type', 'application/xml; charset=utf-8')
  setHeader(event, 'cache-control', 'public, no-cache')
  return '<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
    + urls.map(u => `  <url><loc>${xml(u.loc)}</loc>${u.lastmod ? `<lastmod>${xml(u.lastmod)}</lastmod>` : ''}</url>`).join('\n')
    + '\n</urlset>\n'
})
