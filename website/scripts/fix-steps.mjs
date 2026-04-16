#!/usr/bin/env node
// Convert the migration script's `1. **Title**` step markers into `### Title`
// headings and dedent the 3-space-indented body that followed.
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
  let inStep = false;
  for (const line of lines) {
    const m = line.match(/^(\s*)1\. \*\*(.+?)\*\*\s*$/);
    if (m) {
      out.push(`### ${m[2]}`);
      inStep = true;
      continue;
    }
    if (inStep) {
      // Dedent up to 3 leading spaces from following indented lines.
      if (/^   \S/.test(line)) {
        out.push(line.slice(3));
        continue;
      }
      if (/^\s*$/.test(line)) {
        out.push(line);
        continue;
      }
      // Non-indented, non-blank line → end of step body.
      inStep = false;
    }
    out.push(line);
  }
  return out.join('\n');
}

const files = walk(ROOT);
let changed = 0;
for (const f of files) {
  const before = fs.readFileSync(f, 'utf8');
  const after = fix(before);
  if (after !== before) {
    fs.writeFileSync(f, after);
    changed++;
    console.log('fixed', path.relative(process.cwd(), f));
  }
}
console.log(`${changed}/${files.length} files touched`);
