#!/usr/bin/env node
// De-indent content inside <TabItem>...</TabItem> blocks.
// MDX treats 4-space-indented lines as code and refuses to parse inline
// component children, so <Tabs>/<TabItem> content must be flush-left.
import fs from 'node:fs';
import path from 'node:path';

const ROOT = path.resolve(process.cwd(), 'src/content/docs');
function walk(dir) {
  const out = [];
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) out.push(...walk(p));
    else if (e.name.endsWith('.mdx')) out.push(p);
  }
  return out;
}

function fix(src) {
  const lines = src.split('\n');
  const out = [];
  let inTab = false;
  let tabsDepth = 0;
  for (const line of lines) {
    const trimmed = line.trim();
    if (/^<Tabs[\s>]/.test(trimmed)) {
      tabsDepth++;
      out.push(trimmed); // flush Tabs tag
      continue;
    }
    if (trimmed === '</Tabs>') {
      tabsDepth = Math.max(0, tabsDepth - 1);
      out.push(trimmed);
      continue;
    }
    if (tabsDepth > 0) {
      if (/^<TabItem[\s>]/.test(trimmed)) {
        inTab = true;
        out.push(trimmed);
        continue;
      }
      if (trimmed === '</TabItem>') {
        inTab = false;
        out.push(trimmed);
        continue;
      }
      if (inTab) {
        // Strip leading whitespace so nothing sits at 4+ spaces.
        out.push(line.replace(/^\s+/, ''));
        continue;
      }
      // Stray content directly inside <Tabs> (shouldn't exist) — leave alone.
    }
    out.push(line);
  }
  return out.join('\n');
}

const files = walk(ROOT);
let changed = 0;
for (const f of files) {
  const before = fs.readFileSync(f, 'utf8');
  if (!before.includes('<Tabs>')) continue;
  const after = fix(before);
  if (after !== before) {
    fs.writeFileSync(f, after);
    changed++;
    console.log('fixed', path.relative(process.cwd(), f));
  }
}
console.log(`${changed} files touched`);
