#!/usr/bin/env node
// One-time migration: extract the 26 top-level `v-if page==='X'` branches from
// web/static/legacy/index.html into web/static/legacy/pages/X.html and replace
// them with <page-host>. Idempotent: if <page-host> is already present, exits 0.
import { parse } from '@vue/compiler-dom'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '../..')
const indexFile = path.join(repo, 'web/static/legacy/index.html')
const pagesDir = path.join(repo, 'web/static/legacy/pages')

const html = fs.readFileSync(indexFile, 'utf8')
if (html.includes('<page-host')) {
  console.log('already split (page-host present)')
  process.exit(0)
}

function findNode (node, pred, out = []) {
  if (!node || typeof node !== 'object') return out
  if (pred(node)) out.push(node)
  if (Array.isArray(node.children)) for (const c of node.children) findNode(c, pred, out)
  return out
}

const ast = parse(html, { comments: true })
const tabMain = findNode(ast, (n) => n.type === 1 && n.tag === 'div' &&
  (n.props || []).some((p) => p.type === 6 && p.name === 'class' && p.value && p.value.content === 'tab-main'))[0]
if (!tabMain) throw new Error('tab-main not found')
const transition = tabMain.children.find((c) => c.type === 1)
if (!transition) throw new Error('transition not found')

const pages = []
for (const node of transition.children) {
  if (node.type !== 1) continue
  const dir = (node.props || []).find((p) => p.type === 7 && (p.name === 'if' || p.name === 'else-if'))
  const exp = dir && dir.exp && String(dir.exp.content || '')
  const m = exp.match(/page\s*===\s*'([^']+)'/)
  if (!m) throw new Error('unexpected page branch: ' + exp)
  pages.push({ name: m[1], node })
}
if (pages.length !== 26) throw new Error('expected 26 pages, got ' + pages.length)

fs.mkdirSync(pagesDir, { recursive: true })
for (const { name, node } of pages) {
  let frag = html.slice(node.loc.start.offset, node.loc.end.offset)
  // Strip the branch conditional from the root element only.
  frag = frag.replace(/^(\<div[^>]*?)\s+v-(?:else-)?if="page==='[^']+'"\s*([^>]*>)/, '$1 $2')
  if (/v-(?:else-)?if="page===/.test(frag.slice(0, 200))) throw new Error('failed to strip v-if for ' + name)
  fs.writeFileSync(path.join(pagesDir, name + '.html'), frag.trimEnd() + '\n')
  console.log('page', name, frag.length)
}

const start = pages[0].node.loc.start.offset
const end = pages[pages.length - 1].node.loc.end.offset
const next = html.slice(0, start) + '<page-host :key="page" :page="page"></page-host>' + html.slice(end)
fs.writeFileSync(indexFile, next)
console.log('rewrote index.html; pages:', pages.length)
