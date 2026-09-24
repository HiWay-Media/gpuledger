#!/usr/bin/env node
// Builds site/dist/index.html from README.md.
//
// The page has no content of its own: every word on it comes from README.md or
// from the filesystem. Design lives here, prose lives there — the repo does not
// get a second copy of itself to keep in sync.
//
//   node site/build.mjs [--out site/dist]

import { marked } from 'marked'
import { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const OUT = resolve(ROOT, process.argv.includes('--out') ? process.argv[process.argv.indexOf('--out') + 1] : 'site/dist')
const REPO = 'https://github.com/hiway-media/gpuledger'
const BLOB = `${REPO}/blob/main`
const SITE = 'https://hiway-media.github.io/gpuledger/'

// The one version lives in VERSION; package.json here is tooling only.
const pkg = { name: 'gpuledger', version: readFileSync(join(ROOT, 'VERSION'), 'utf8').trim() }
const md = readFileSync(join(ROOT, 'README.md'), 'utf8')

// One logo, three consumers: the favicon (inlined), the header, the hero.
const logo = readFileSync(join(ROOT, 'assets', 'logo.svg'), 'utf8')
const favicon = `data:image/svg+xml,${encodeURIComponent(logo.replace(/\n\s*/g, '').replace(/<title>.*?<\/title>/, ''))}`
// The <svg> tag's own width/height go, or the inlined tag carries two of each — and
// only that tag's: the same attributes on the five <rect>s are the logo. Stripping them
// from the whole file collapsed every bar and left the page showing the needle alone.
const mark = (size, cls) =>
  logo.replace(/<svg\b[^>]*>/, (tag) => tag.replace(/\s(?:width|height)="[^"]*"/g, '').replace('<svg', `<svg class="${cls}" width="${size}" height="${size}"`))

// The build refuses to emit a logo it has taken apart. A page that ships a blank mark
// looks fine to every test that reads text.
{
  const shapes = (s) => (s.match(/<(?:rect|circle|path)\b[^>]*>/g) ?? []).join('')
  const dims = (s) => (shapes(s).match(/\s(?:width|height|r|d)="/g) ?? []).length
  const got = mark(22, 'x')
  if (dims(got) !== dims(logo) || !/<svg[^>]*\swidth="22"/.test(got)) {
    throw new Error(`site/build.mjs: inlining the logo lost part of it — ${dims(logo)} shape dimensions became ${dims(got)}`)
  }
}

marked.setOptions({ mangle: false, headerIds: false })

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
const slug = (s) =>
  s
    .toLowerCase()
    .replace(/[^\w\s-]/g, '')
    .trim()
    .replace(/\s+/g, '-')

// --- split README into an intro and one entry per H2, fence-aware -----------

function parseReadme(source) {
  const lines = source.split('\n')
  const sections = []
  let title = 'gpuledger'
  let current = { heading: null, lines: [] }
  let fenced = false

  for (const line of lines) {
    if (line.startsWith('```')) fenced = !fenced
    if (!fenced && line.startsWith('# ')) {
      title = line.slice(2).trim()
      continue
    }
    if (!fenced && line.startsWith('## ')) {
      sections.push(current)
      current = { heading: line.slice(3).trim(), lines: [] }
      continue
    }
    current.lines.push(line)
  }
  sections.push(current)

  const intro = sections.shift()
  return { title, intro: intro.lines.join('\n').trim(), sections: sections.map((s) => ({ ...s, body: s.lines.join('\n').trim() })) }
}

// The intro is the lede (first paragraph) and whatever follows it — here the
// status note. The README's own logo <p> is dropped: the page draws its own mark.
function parseIntro(raw) {
  const intro = raw
    .split('\n')
    .filter((l) => !/^\s*<\/?(p|img|div|a|picture|source)\b/i.test(l))
    .join('\n')
    .trim()
  const [lede, ...rest] = intro.split(/\n\n+/)
  return { lede: lede.trim(), after: rest.join('\n\n').trim() }
}

// --- html fragments ---------------------------------------------------------

// A table whose header row is empty renders as a strip of blank cells.
function dropEmptyHead(html) {
  return html.replace(/<thead>[\s\S]*?<\/thead>/g, (thead) => (/>[^<\s][\s\S]*?<\/th>/.test(thead) ? thead : ''))
}

// Bare `path/` and `file.md` references in the README become links to the repo.
function linkifyPaths(html) {
  return html.replace(/<code>([\w./-]+\.(?:md|json|mjs)|(?:bin|scripts|test|thoughts)\/[\w./-]*)<\/code>/g, (full, path) => {
    const clean = path.replace(/\/$/, '')
    if (!existsSync(join(ROOT, clean))) return full
    return `<a class="pathlink" href="${BLOB}/${clean}"><code>${path}</code></a>`
  })
}

const renderSection = (s) => {
  const id = slug(s.heading)
  return `  <section id="${id}">
    <h2><a class="anchor" href="#${id}">${esc(s.heading)}</a></h2>
${linkifyPaths(dropEmptyHead(marked.parse(s.body)))}
  </section>`
}

// --- assemble ---------------------------------------------------------------

const { title, intro, sections } = parseReadme(md)
const { lede, after } = parseIntro(intro)

// The README's H1 is `name — tagline`. The name is the wordmark and the <h1>; the
// tagline sits under it. Splitting here is what keeps the page from repeating the
// tagline twice and from hard-coding either half.
const [name, tagline = ''] = title.split(/\s+—\s+/)
// The wordmark: the tail of the name in the accent colour, the way the sibling
// repos split theirs. `gpuledger` → transcript·meter. A lazy `.+?` so a name
// that is only the suffix keeps a head to colour against.
const wordmark = /^(.+?)(meter|gate|hook|lens|sim)$/i.exec(name)
const brandHtml = wordmark ? `${esc(wordmark[1])}<span>${esc(wordmark[2])}</span>` : esc(name)

// A meta description is a sentence, not a paragraph: markdown out, one sentence,
// cut on a word boundary if it still runs long.
const description = (() => {
  const flat = lede.replace(/\*\*/g, '').replace(/`/g, '').replace(/\s+/g, ' ').trim()
  const sentence = flat.split(/(?<=[a-z0-9)])\.\s/)[0].replace(/\.$/, '').concat('.')
  if (sentence.length <= 160) return sentence
  return sentence.slice(0, 157).replace(/\s+\S*$/, '') + '…'
})()

// License is one word — the footer already carries it. Prior art stays in the page
// but out of the nav, so the page ends on credits, not on an appendix.
const body = sections.filter((s) => !/^license$/i.test(s.heading))
const nav = body.filter((s) => !/^prior art$/i.test(s.heading))
const rendered = body.map(renderSection)

// One derived headline, reused by <title>, Open Graph, Twitter and JSON-LD, so
// the four can never drift apart. Like everything else on the page, the words
// come from README.md — the generator adds none of its own.
const headline = title

// A social card is shipped only when it exists: a <meta og:image> pointing at a
// 404 is worse than none, and this one is a PNG rendered from
// assets/social-preview.html with headless Chrome, not a build product.
const socialCard = existsSync(join(ROOT, 'assets', 'social-preview.png'))
const cardTags = socialCard
  ? `<meta property="og:image" content="${SITE}assets/social-preview.png">
<meta property="og:image:width" content="1280">
<meta property="og:image:height" content="640">
<meta property="og:image:alt" content="${esc(headline)}">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:image" content="${SITE}assets/social-preview.png">
<meta name="twitter:image:alt" content="${esc(headline)}">`
  : `<meta name="twitter:card" content="summary">`

// Structured data. The strings are the same two the meta tags use; nothing here
// is written for the crawler that is not already on the page.
const jsonLd = JSON.stringify({
  '@context': 'https://schema.org',
  '@graph': [
    {
      '@type': 'WebSite',
      '@id': `${SITE}#website`,
      url: SITE,
      name: title,
      description,
      inLanguage: 'en',
    },
    {
      '@type': 'SoftwareApplication',
      '@id': `${SITE}#cli`,
      name: title,
      description,
      url: SITE,
      applicationCategory: 'DeveloperApplication',
      operatingSystem: 'macOS, Linux, Windows',
      softwareVersion: pkg.version,
      codeRepository: REPO,
      license: 'https://opensource.org/licenses/MIT',
      author: { '@type': 'Person', name: 'Allan Nava' },
      offers: { '@type': 'Offer', price: '0', priceCurrency: 'USD' },
    },
  ],
}).replace(/</g, '\\u003c')

const html = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(headline)}</title>
<meta name="description" content="${esc(description)}">
<link rel="canonical" href="${SITE}">
<meta name="theme-color" content="#0b7285" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#1b1a18" media="(prefers-color-scheme: dark)">
<meta property="og:type" content="website">
<meta property="og:site_name" content="${esc(title)}">
<meta property="og:locale" content="en">
<meta property="og:url" content="${SITE}">
<meta property="og:title" content="${esc(headline)}">
<meta property="og:description" content="${esc(description)}">
<meta name="twitter:title" content="${esc(headline)}">
<meta name="twitter:description" content="${esc(description)}">
${cardTags}
<link rel="icon" href="${favicon}">
<link rel="apple-touch-icon" href="assets/logo.svg">
<script type="application/ld+json">${jsonLd}</script>
<style>
:root {
  --bg: #fbfaf8; --panel: #fff; --line: #e6e1d9; --ink: #1b1a18; --muted: #6b665e;
  --accent: #0b7285; --accent-soft: #e3f6f9; --code-bg: #f4f1ec;
  --mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  --sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, Helvetica, Arial, sans-serif;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #100f0e; --panel: #171614; --line: #2c2925; --ink: #ece8e1; --muted: #9b948a;
    --accent: #7fd3e0; --accent-soft: #122226; --code-bg: #1c1a17;
  }
}
* { box-sizing: border-box; }
html { scroll-behavior: smooth; scroll-padding-top: 5rem; }
body {
  margin: 0; background: var(--bg); color: var(--ink);
  font: 400 17px/1.65 var(--sans); -webkit-font-smoothing: antialiased;
}
.wrap { max-width: 62rem; margin: 0 auto; padding: 0 1.5rem; }
a { color: var(--accent); text-decoration-thickness: 1px; text-underline-offset: 2px; }
code { font-family: var(--mono); font-size: .88em; }
:not(pre) > code { background: var(--code-bg); padding: .12em .38em; border-radius: 4px; }
pre {
  background: var(--code-bg); border: 1px solid var(--line); border-radius: 10px;
  padding: 1rem 1.1rem; overflow-x: auto; font-size: .86rem; line-height: 1.6;
}
pre code { background: none; padding: 0; }

/* header */
header.top {
  position: sticky; top: 0; z-index: 10; backdrop-filter: blur(10px);
  background: var(--bg); background: color-mix(in srgb, var(--bg) 86%, transparent);
  border-bottom: 1px solid var(--line);
}
header.top .wrap { display: flex; align-items: center; gap: 1.5rem; height: 3.75rem; }
.brand { display: inline-flex; align-items: center; font-weight: 650; letter-spacing: .06em; color: var(--ink); text-decoration: none; }
.brand-mark { border-radius: 5px; margin-right: .55rem; }
.hero-mark { display: block; margin-bottom: 1.5rem; border-radius: 15px; }
.brand span { color: var(--accent); }
header.top nav { margin-left: auto; display: flex; gap: 1.15rem; flex-wrap: wrap; }
header.top nav a { color: var(--muted); text-decoration: none; font-size: .88rem; }
header.top nav a:hover { color: var(--ink); }

/* hero */
.hero { padding: 4.5rem 0 2.5rem; }
.eyebrow {
  display: inline-block; font: 600 .72rem/1 var(--mono); letter-spacing: .14em; text-transform: uppercase;
  color: var(--accent); background: var(--accent-soft); border-radius: 99px; padding: .45rem .8rem; margin-bottom: 1.5rem;
}
.hero h1 { font-size: clamp(2.6rem, 7vw, 4.2rem); line-height: 1; margin: 0 0 .75rem; letter-spacing: -.03em; }
.tagline { font-size: clamp(1.15rem, 2.6vw, 1.5rem); line-height: 1.3; color: var(--ink); margin: 0 0 1.25rem; max-width: 40rem; letter-spacing: -.01em; }
.lede { font-size: clamp(1.05rem, 2.2vw, 1.28rem); color: var(--muted); max-width: 46rem; margin: 0 0 1.75rem; }
.lede strong { color: var(--ink); font-weight: 600; }
.cta { display: flex; gap: .7rem; flex-wrap: wrap; margin-bottom: 3rem; }
.cta a {
  display: inline-flex; align-items: center; gap: .5rem; text-decoration: none; font-size: .93rem; font-weight: 550;
  padding: .62rem 1.1rem; border-radius: 8px; border: 1px solid var(--line); color: var(--ink); background: var(--panel);
}
.cta a.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
.cta a:hover { border-color: var(--accent); }

/* sections */
section { padding: 3.25rem 0; border-top: 1px solid var(--line); }
section h2 { font-size: 1.55rem; letter-spacing: -.02em; margin: 0 0 1.25rem; }
section h2 .anchor { color: inherit; text-decoration: none; }
section h2 .anchor:hover::after { content: " #"; color: var(--accent); }
section h3 { font-size: 1.05rem; margin: 2rem 0 .5rem; }
section p, section li { max-width: 48rem; }
table { border-collapse: collapse; width: 100%; margin: 1.25rem 0; font-size: .93rem; display: block; overflow-x: auto; }
th, td { text-align: left; padding: .62rem .8rem; border-bottom: 1px solid var(--line); vertical-align: top; }
th { font-size: .78rem; text-transform: uppercase; letter-spacing: .07em; color: var(--muted); }
blockquote { margin: 1.25rem 0; padding: .1rem 0 .1rem 1.1rem; border-left: 3px solid var(--accent); color: var(--muted); }

/* copy button */
.codeblock { position: relative; }
.copy {
  position: absolute; top: .55rem; right: .55rem; font: 500 .72rem var(--sans); cursor: pointer;
  background: var(--panel); color: var(--muted); border: 1px solid var(--line); border-radius: 6px; padding: .25rem .55rem;
  opacity: 0; transition: opacity .15s;
}
.codeblock:hover .copy, .copy:focus { opacity: 1; }
.copy:hover { color: var(--accent); border-color: var(--accent); }

footer { border-top: 1px solid var(--line); padding: 2.5rem 0 4rem; color: var(--muted); font-size: .88rem; }
footer a { color: var(--muted); }
footer .row { display: flex; gap: 1.25rem; flex-wrap: wrap; }
@media (prefers-reduced-motion: reduce) { html { scroll-behavior: auto; } }
@media (max-width: 640px) { .hero { padding-top: 3rem; } header.top nav a:not(.gh) { display: none; } }
</style>
</head>
<body>
<header class="top">
  <div class="wrap">
    <a class="brand" href="#top">${mark(22, 'brand-mark')}${brandHtml}</a>
    <nav>
${nav.map((s) => `      <a href="#${slug(s.heading)}">${esc(s.heading)}</a>`).join('\n')}
      <a class="gh" href="${REPO}">GitHub</a>
    </nav>
  </div>
</header>

<main class="wrap" id="top">
  <div class="hero">
    ${mark(66, 'hero-mark')}
    <span class="eyebrow">Nomad · NVIDIA · one static binary · v${esc(pkg.version)}</span>
    <h1>${esc(name)}</h1>
    ${tagline ? `<p class="tagline">${esc(tagline)}</p>` : ''}
    <div class="lede">${marked.parseInline(lede.replace(/\n/g, ' '))}</div>
    <div class="cta">
      <a class="primary" href="#install">Install</a>
      <a href="${REPO}">Source</a>
      <a href="${REPO}/releases">releases</a>
    </div>
    <div class="note">${marked.parse(after)}</div>
  </div>

${rendered.join('\n\n')}
</main>

<footer>
  <div class="wrap row">
    <span>MIT · <a href="${REPO}">hiway-media/gpuledger</a></span>
    <span>Generated from <a href="${BLOB}/README.md">README.md</a></span>
  </div>
</footer>

<script>
for (const pre of document.querySelectorAll('pre')) {
  const box = document.createElement('div')
  box.className = 'codeblock'
  pre.parentNode.insertBefore(box, pre)
  box.appendChild(pre)
  const btn = document.createElement('button')
  btn.className = 'copy'
  btn.textContent = 'copy'
  btn.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(pre.innerText)
      btn.textContent = 'copied'
      setTimeout(() => { btn.textContent = 'copy' }, 1400)
    } catch { btn.textContent = 'failed' }
  })
  box.appendChild(btn)
}
</script>
</body>
</html>
`

mkdirSync(OUT, { recursive: true })
cpSync(join(ROOT, 'assets'), join(OUT, 'assets'), { recursive: true, filter: (src) => !src.endsWith('.html') })
writeFileSync(join(OUT, 'index.html'), html)
writeFileSync(join(OUT, '.nojekyll'), '')

// One page, so one URL. Section anchors are fragments of this document, not
// separate resources, and listing them would misdescribe the site. lastmod is
// the build date because Pages deploys on push to main — the page really was
// regenerated then.
//
// There is deliberately NO robots.txt here. Crawlers read robots.txt only from
// the host root, and https://allan-nava.github.io/robots.txt already speaks for
// every project site under it: its Sitemap: lines are generated in CI from the
// Allan-Nava Pages sites whose sitemap.xml returns 200. Shipping this file is
// what gets gpuledger listed there; a robots.txt at /gpuledger/ would be dead weight.
const lastmod = new Date().toISOString().slice(0, 10)
writeFileSync(
  join(OUT, 'sitemap.xml'),
  `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>${SITE}</loc>
    <lastmod>${lastmod}</lastmod>
    <changefreq>weekly</changefreq>
    <priority>1.0</priority>
  </url>
</urlset>
`,
)

console.log(`built ${join(OUT, 'index.html')} — ${(html.length / 1024).toFixed(1)} kB, ${rendered.length} sections, + sitemap.xml`)
