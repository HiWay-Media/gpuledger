#!/usr/bin/env node
// backlog.mjs — lint BACKLOG.md, generate ROADMAP.md, keep the GitHub issues in step.
//
// BACKLOG.md is the single source of trugl: every planned item lives there with a
// stable GL-n id and a trailing `<!-- gl: ... -->` metadata comment. ROADMAP.md and
// the GitHub issues are generated views of the same data.
//
//   node scripts/backlog.mjs lint       validate BACKLOG.md (ids, metadata, milestones)
//   node scripts/backlog.mjs roadmap    regenerate ROADMAP.md
//   node scripts/backlog.mjs check      fail if ROADMAP.md is stale (CI gate)
//   node scripts/backlog.mjs stats      one-line summary
//   node scripts/backlog.mjs issues     plan the GitHub issue sync; touches nothing
//                                       (create, retitle, move to the right milestone, close, reopen)
//     --apply                           execute the plan
//     --milestones v0.1.0,v0.2.0        limit to these milestones (by version)
//
// Node 18+, no dependencies — the repository has none and neither does its tooling.
//
// BACKLOG_FILE, ROADMAP_FILE and BACKLOG_ISSUES_SNAPSHOT override the two paths and
// the source of "which issues exist already", so the planner can be tested against
// a fixture without a network call and without touching a public repository.

import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const BACKLOG = process.env.BACKLOG_FILE ?? join(ROOT, 'BACKLOG.md')
const ROADMAP = process.env.ROADMAP_FILE ?? join(ROOT, 'ROADMAP.md')
const REPO_BLOB = 'https://github.com/hiway-media/gpuledger/blob/main'
const DASH = ' — '

const PRIOS = ['high', 'med', 'low']
const SIZES = ['S', 'M', 'L', 'XL']
const PHASES = ['now', 'next', 'later', 'shipped']
const LABELS = {
  collector: ['b7552f', 'Reading NVML/nvidia-smi, containers, Nomad'],
  ledger: ['1d76db', 'The join: who holds which GPU, and the findings'],
  benchmark: ['0e8a16', 'Measurement and evals'],
  release: ['5319e7', 'Publishing and versioning'],
  docs: ['0075ca', 'README, CONTRIBUTING, site'],
  project: ['6a737d', 'Backlog, roadmap, repo hygiene'],
  tests: ['d4c5f9', 'Test coverage and test tooling'],
  enhancement: ['a2eeef', 'New capability'],
  'prio-high': ['b60205', 'High priority in BACKLOG.md'],
  'prio-med': ['fbca04', 'Medium priority in BACKLOG.md'],
  'prio-low': ['c2e0c6', 'Low priority in BACKLOG.md'],
}

// --- parse ------------------------------------------------------------------

const meta = (s, tag) => {
  const m = s.match(new RegExp(`<!--\\s*${tag}:([\\s\\S]*?)-->`))
  if (!m) return null
  const out = {}
  for (const kv of m[1].trim().split(/\s+/).filter(Boolean)) {
    const [k, ...v] = kv.split('=')
    out[k] = v.join('=')
  }
  return out
}

export function parse(text) {
  const errors = []
  const err = (line, msg) => errors.push(`BACKLOG.md:${line}: ${msg}`)
  const milestones = []
  const items = []
  let ms = null
  let fenced = false
  let cur = null
  const lines = text.split('\n')

  const flush = () => {
    if (!cur) return
    const raw = cur.lines.join(' ').replace(/\s+/g, ' ').trim()
    const m = raw.match(/^- \[( |x)\] \*\*(GL-(\d+))\s+—\s+(.+?)\*\*:?\s*([\s\S]*)$/)
    if (!m) err(cur.line, 'item does not match `- [ ] **GL-n — Title**: body <!-- gl: ... -->`')
    else {
      const hg = meta(raw, 'gl')
      // Strip every HTML comment, including an unterminated one, so nothing that reads
      // as markup survives into an issue body. Loop: one pass can leave a `<!--` behind.
      let body = m[5]
      for (let prev = null; prev !== body; ) {
        prev = body
        body = body.replace(/<!--[\s\S]*?(?:-->|$)/g, '')
      }
      body = body.trim()
      items.push({ line: cur.line, status: m[1] === 'x' ? 'shipped' : 'open', id: m[2], num: Number(m[3]), title: m[4].trim(), body, meta: hg, ms })
    }
    cur = null
  }

  lines.forEach((line, i) => {
    const n = i + 1
    if (line.startsWith('```')) {
      fenced = !fenced
      flush()
      return
    }
    if (fenced) return
    const h = line.match(/^## (.+?)\s*(<!--[\s\S]*-->)?\s*$/)
    if (h) {
      flush()
      const mm = meta(line, 'ms')
      if (mm) {
        const title = h[1].trim()
        const version = title.match(/^(v\d+\.\d+\.\d+)\b/)?.[1] ?? null
        ms = { line: n, title, version, phase: mm.phase ?? null, items: [] }
        milestones.push(ms)
      } else ms = null
      return
    }
    if (/^- \[[ x]\] \*\*GL-/.test(line)) {
      flush()
      cur = { line: n, lines: [line] }
      return
    }
    if (cur && /^\s+\S/.test(line)) {
      cur.lines.push(line.trim())
      return
    }
    flush()
  })
  flush()

  for (const it of items) if (it.ms) it.ms.items.push(it)
  return { milestones, items, errors }
}

export function lint(model) {
  const errors = [...model.errors]
  const err = (line, msg) => errors.push(`BACKLOG.md:${line}: ${msg}`)
  const seen = new Map()
  for (const m of model.milestones) {
    if (!m.version) err(m.line, `milestone heading must start with a version, vX.Y.Z — Theme: "${m.title}"`)
    if (!PHASES.includes(m.phase)) err(m.line, `milestone phase must be one of ${PHASES.join('|')}, got "${m.phase}"`)
  }
  for (const it of model.items) {
    if (seen.has(it.id)) err(it.line, `${it.id} is already used on line ${seen.get(it.id)}`)
    seen.set(it.id, it.line)
    if (!it.ms) err(it.line, `${it.id} is not under a milestone heading`)
    if (!it.meta) {
      err(it.line, `${it.id} has no <!-- gl: ... --> metadata`)
      continue
    }
    if (!PRIOS.includes(it.meta.prio)) err(it.line, `${it.id}: prio must be ${PRIOS.join('|')}`)
    if (!SIZES.includes(it.meta.size)) err(it.line, `${it.id}: size must be ${SIZES.join('|')}`)
    const labels = (it.meta.labels ?? '').split(',').filter(Boolean)
    if (!labels.length) err(it.line, `${it.id}: at least one label`)
    for (const l of labels) if (!LABELS[l] || l.startsWith('prio-')) err(it.line, `${it.id}: unknown label "${l}"`)
    if (it.status === 'shipped' && !it.meta.ver) err(it.line, `${it.id} is shipped but has no ver=`)
    if (it.status === 'open' && it.meta.ver) err(it.line, `${it.id} is open but carries ver=${it.meta.ver}`)
    if (!it.body) err(it.line, `${it.id} has no body`)
  }
  return errors
}

// --- roadmap ----------------------------------------------------------------

export function roadmap(model) {
  const all = model.items
  const shipped = all.filter((i) => i.status === 'shipped').length
  const bar = (done, total) => {
    const n = total ? Math.round((10 * done) / total) : 0
    return `\`${'#'.repeat(n)}${'.'.repeat(10 - n)}\` ${total ? Math.round((100 * done) / total) : 0}%`
  }
  const out = []
  out.push('# Roadmap — gpuledger', '', '<!-- GENERATED by scripts/backlog.mjs roadmap — do not edit by hand. -->', '')
  out.push('> This page is **generated** from [BACKLOG.md](BACKLOG.md), the single source of truth for planned work. Regenerate it with `node scripts/backlog.mjs roadmap` after editing the backlog — CI fails when the two disagree.', '')
  out.push(`**${all.length} items · ${shipped} shipped · ${all.length - shipped} open · ${model.milestones.length} milestones.**`, '')
  out.push('## At a glance', '', '| Milestone | Phase | Progress | Open | Shipped |', '|---|---|---|---|---|')
  for (const m of model.milestones) {
    const s = m.items.filter((i) => i.status === 'shipped').length
    out.push(`| **${m.title}** | ${m.phase} | ${bar(s, m.items.length)} | ${m.items.length - s} | ${s} |`)
  }
  out.push('')
  for (const m of model.milestones) {
    out.push(`## ${m.title}`, '')
    for (const it of m.items) {
      const tick = it.status === 'shipped' ? 'x' : ' '
      const ver = it.meta?.ver ? ` · \`${it.meta.ver}\`` : ''
      out.push(`- [${tick}] **${it.id}** — ${it.title} · ${it.meta?.prio ?? '?'} · ${it.meta?.size ?? '?'} · ${(it.meta?.labels ?? '').split(',').join(', ')}${ver}`)
    }
    out.push('')
  }
  return out.join('\n')
}

// --- issues -----------------------------------------------------------------

const sh = (bin, args) => execFileSync(bin, args, { encoding: 'utf8' })

// `<id>\t<number>\t<state>\t<title>` for the issues that already exist. The id is
// the title prefix, the only durable link back to the backlog.
function existingIssues() {
  let rows
  if (process.env.BACKLOG_ISSUES_SNAPSHOT) rows = readFileSync(process.env.BACKLOG_ISSUES_SNAPSHOT, 'utf8').trim().split('\n').filter(Boolean).map((l) => l.split('\t'))
  else {
    const list = JSON.parse(sh('gh', ['issue', 'list', '--state', 'all', '--limit', '500', '--json', 'number,title,state,milestone']))
    rows = list.map((i) => {
      const id = i.title.split(DASH)[0]
      return [id, String(i.number), i.state.toLowerCase(), i.title, i.milestone?.title ?? '']
    })
  }
  const map = new Map()
  for (const [id, num, state, title, milestone] of rows) if (/^GL-\d+$/.test(id)) map.set(id, { num, state, title, milestone })
  return map
}

// One action per item:
//   CREATE  open item with no issue        REOPEN  open item whose issue is closed
//   CLOSE   shipped item whose issue is open  RETITLE title drifted (any state)
//   OK      already right                  SKIP    shipped and never had an issue
//   MILESTONE  the issue sits under a different milestone than the item's heading
export function plan(model, existing, only = []) {
  const actions = []
  for (const it of model.items) {
    if (only.length && !only.includes(it.ms?.version)) continue
    const ex = existing.get(it.id)
    const want = `${it.id}${DASH}${it.title}`
    if (ex && ex.title !== want) actions.push(['RETITLE', it.id, ex.num])
    if (ex && ex.milestone !== undefined && it.ms && ex.milestone !== it.ms.title) actions.push(['MILESTONE', it.id, ex.num])
    if (it.status === 'open') {
      if (!ex) actions.push(['CREATE', it.id, '-'])
      else if (ex.state === 'closed') actions.push(['REOPEN', it.id, ex.num])
      else actions.push(['OK', it.id, ex.num])
    } else {
      if (!ex) actions.push(['SKIP', it.id, '-'])
      else if (ex.state === 'open') actions.push(['CLOSE', it.id, ex.num])
      else actions.push(['OK', it.id, ex.num])
    }
  }
  return actions
}

function issueBody(it) {
  return `${it.body}

---

Planned work, tracked in [BACKLOG.md](${REPO_BLOB}/BACKLOG.md) as \`${it.id}\` under **${it.ms.title}** (priority ${it.meta.prio}, size ${it.meta.size}).

\`BACKLOG.md\` is the single source of trugl: it carries the stable \`GL-n\` id that commits and the CHANGELOG reference, and [ROADMAP.md](${REPO_BLOB}/ROADMAP.md) is generated from it. This issue is a view of that item, kept in step by \`scripts/backlog.mjs issues --apply\`, so closing it means ticking the item in the backlog and regenerating the roadmap in the same commit.
`
}

function ensureLabels() {
  const have = new Set(JSON.parse(sh('gh', ['label', 'list', '--limit', '200', '--json', 'name'])).map((l) => l.name))
  for (const [name, [color, desc]] of Object.entries(LABELS)) {
    if (have.has(name)) continue
    sh('gh', ['label', 'create', name, '--color', color, '--description', desc])
    console.log(`  created label ${name}`)
  }
}

function ensureMilestone(title) {
  const have = JSON.parse(sh('gh', ['api', 'repos/:owner/:repo/milestones?state=all&per_page=100'])).map((m) => m.title)
  if (have.includes(title)) return
  sh('gh', ['api', 'repos/:owner/:repo/milestones', '-f', `title=${title}`, '-f', 'description=Backlog milestone. Source of truth: BACKLOG.md'])
  console.log(`  created milestone ${title}`)
}

function apply(model, actions) {
  const byId = new Map(model.items.map((i) => [i.id, i]))
  ensureLabels()
  for (const [action, id, num] of actions) {
    const it = byId.get(id)
    const title = `${id}${DASH}${it.title}`
    if (action === 'CREATE') {
      ensureMilestone(it.ms.title)
      const labels = [...it.meta.labels.split(','), `prio-${it.meta.prio}`].flatMap((l) => ['--label', l])
      const url = sh('gh', ['issue', 'create', '--title', title, '--body', issueBody(it), '--milestone', it.ms.title, ...labels]).trim()
      console.log(`  created ${id}  ${url}`)
    } else if (action === 'RETITLE') {
      sh('gh', ['issue', 'edit', num, '--title', title])
      console.log(`  retitled ${id}  #${num}  -> ${title}`)
    } else if (action === 'MILESTONE') {
      ensureMilestone(it.ms.title)
      sh('gh', ['issue', 'edit', num, '--milestone', it.ms.title])
      console.log(`  moved ${id}  #${num}  -> ${it.ms.title}`)
    } else if (action === 'CLOSE') {
      sh('gh', ['issue', 'close', num, '--comment', 'Shipped: the backlog item is ticked in BACKLOG.md. Closed by `scripts/backlog.mjs issues --apply`.'])
      console.log(`  closed ${id}  #${num}`)
    } else if (action === 'REOPEN') {
      sh('gh', ['issue', 'reopen', num, '--comment', 'Reopened: the backlog item is open again in BACKLOG.md. Reopened by `scripts/backlog.mjs issues --apply`.'])
      console.log(`  reopened ${id}  #${num}`)
    }
  }
}

// --- main -------------------------------------------------------------------

function main() {
  const [cmd = 'help', ...rest] = process.argv.slice(2)
  const model = parse(readFileSync(BACKLOG, 'utf8'))
  const errors = lint(model)
  if (errors.length && cmd !== 'help') {
    for (const e of errors) console.error(e)
    process.exit(1)
  }
  switch (cmd) {
    case 'lint':
      console.log(`ok — ${model.items.length} items, ${model.milestones.length} milestones`)
      break
    case 'roadmap':
      writeFileSync(ROADMAP, roadmap(model))
      console.log(`wrote ${ROADMAP}`)
      break
    case 'check': {
      let current = ''
      try {
        current = readFileSync(ROADMAP, 'utf8')
      } catch {}
      if (current !== roadmap(model)) {
        console.error('ROADMAP.md is stale — run `node scripts/backlog.mjs roadmap` and commit the result')
        process.exit(1)
      }
      console.log('ok — ROADMAP.md is in step with BACKLOG.md')
      break
    }
    case 'stats': {
      const s = model.items.filter((i) => i.status === 'shipped').length
      console.log(`${model.items.length} items · ${s} shipped · ${model.items.length - s} open · ${model.milestones.length} milestones`)
      break
    }
    case 'issues': {
      const doApply = rest.includes('--apply')
      const mi = rest.indexOf('--milestones')
      const only = mi >= 0 ? rest[mi + 1].split(',').filter(Boolean) : []
      const actions = plan(model, existingIssues(), only)
      for (const a of actions) console.log(a.join('\t'))
      const count = (k) => actions.filter((a) => a[0] === k).length
      console.log(`\n${count('CREATE')} to create · ${count('RETITLE')} to retitle · ${count('MILESTONE')} to move · ${count('CLOSE')} to close · ${count('REOPEN')} to reopen · ${count('OK')} ok · ${count('SKIP')} skipped`)
      if (doApply) apply(model, actions.filter((a) => !['OK', 'SKIP'].includes(a[0])))
      else console.log('(plan only — pass --apply to execute)')
      break
    }
    default:
      console.log(readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n').slice(1, 13).map((l) => l.replace(/^\/\/ ?/, '')).join('\n'))
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main()
