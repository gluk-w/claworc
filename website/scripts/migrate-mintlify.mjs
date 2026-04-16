#!/usr/bin/env node
// One-time Mintlify → Starlight MDX rewrite.
// Walks src/content/docs/**/*.mdx and applies the component mappings
// defined in the consolidation plan.

import fs from 'node:fs';
import path from 'node:path';

const ROOT = path.resolve(process.cwd(), 'src/content/docs');

function walk(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...walk(p));
    else if (entry.name.endsWith('.mdx')) out.push(p);
  }
  return out;
}

const STARLIGHT_IMPORT =
  "import { Aside, Card, CardGrid, LinkCard, Tabs, TabItem, Steps } from '@astrojs/starlight/components';";

function rewrite(src) {
  let s = src;

  // 1. Asides: <Note>/<Warning>/<Info>/<Tip> → <Aside type=...>
  const asideMap = {
    Note: 'note',
    Info: 'note',
    Tip: 'tip',
    Warning: 'caution',
    Danger: 'danger',
  };
  for (const [tag, type] of Object.entries(asideMap)) {
    s = s.replaceAll(new RegExp(`<${tag}>`, 'g'), `<Aside type="${type}">`);
    s = s.replaceAll(new RegExp(`</${tag}>`, 'g'), `</Aside>`);
  }

  // 2. <CardGroup cols={N}> → <CardGrid>
  s = s.replace(/<CardGroup\s+cols=\{\d+\}\s*>/g, '<CardGrid>');
  s = s.replace(/<CardGroup\s*>/g, '<CardGrid>');
  s = s.replaceAll('</CardGroup>', '</CardGrid>');

  // 3. <Card ... href="..."> becomes <LinkCard />, self-closing when empty.
  //    <Card ... > without href stays as <Card ...>, but without `icon`.
  s = s.replace(
    /<Card\s+([^>]*?)\/>/g,
    (_m, attrs) => (attrs.includes('href=') ? `<LinkCard ${attrs}/>` : `<Card ${attrs}/>`),
  );
  s = s.replace(
    /<Card\s+([^>]*?)>([\s\S]*?)<\/Card>/g,
    (_m, attrs, inner) => {
      const trimmed = inner.trim();
      if (attrs.includes('href=') && trimmed.length === 0) {
        return `<LinkCard ${attrs}/>`;
      }
      if (attrs.includes('href=')) {
        // LinkCard supports `description` prop; inline description as a prop when short.
        const desc = trimmed.replace(/"/g, '&quot;').replace(/\s+/g, ' ');
        return `<LinkCard ${attrs} description="${desc}"/>`;
      }
      return `<Card ${attrs}>${inner}</Card>`;
    },
  );
  // Strip Mintlify-only `icon="..."` from Card props (Starlight Card uses `icon` differently)
  s = s.replace(/(<(?:Card|LinkCard)[^>]*?)\sicon="[^"]*"/g, '$1');

  // 4. Tabs: <Tab title="X"> → <TabItem label="X">
  s = s.replaceAll(/<Tab\s+title="([^"]*)"\s*>/g, '<TabItem label="$1">');
  s = s.replaceAll('</Tab>', '</TabItem>');

  // 5. Steps: <Step title="X">body</Step> → - ### X\n  body
  //    Starlight <Steps> wraps an ordered list. We convert each <Step> to
  //    a list item whose first child is a heading derived from `title`.
  s = s.replace(
    /<Step\s+title="([^"]*)"\s*>([\s\S]*?)<\/Step>/g,
    (_m, title, body) => {
      // Indent inner body by 2 spaces so it becomes part of the list item.
      const indented = body
        .trim()
        .split('\n')
        .map((l) => (l.length ? '   ' + l : l))
        .join('\n');
      return `1. **${title}**\n\n${indented}\n`;
    },
  );

  // 6. <Frame>...</Frame> wrappers: drop the wrapper, keep inner content.
  s = s.replace(/<Frame[^>]*>([\s\S]*?)<\/Frame>/g, (_m, inner) => inner.trim());

  // 7. <Check>...</Check> → ✓ inline
  s = s.replace(/<Check>([\s\S]*?)<\/Check>/g, '✓ $1');

  // 8. <CodeGroup> → <Tabs> wrapper. Per-block ```lang title="X" becomes TabItem label="X".
  s = s.replace(/<CodeGroup>([\s\S]*?)<\/CodeGroup>/g, (_m, inner) => {
    const blocks = inner
      .trim()
      .split(/(?=```)/)
      .map((b) => b.trim())
      .filter(Boolean);
    const tabItems = blocks.map((b) => {
      const titleMatch = b.match(/```\w+\s+title="([^"]+)"/);
      const label = titleMatch ? titleMatch[1] : 'Code';
      return `<TabItem label="${label}">\n\n${b}\n\n</TabItem>`;
    });
    return `<Tabs>\n${tabItems.join('\n')}\n</Tabs>`;
  });

  // 9. Image-path rewrite: /images/foo.png → /docs-images/foo.png
  s = s.replaceAll('/images/', '/docs-images/');

  // 10. Prepend the Starlight import once (after frontmatter block).
  if (!s.includes('@astrojs/starlight/components')) {
    s = s.replace(
      /^(---[\s\S]*?---\n)/,
      `$1\n${STARLIGHT_IMPORT}\n`,
    );
  }

  return s;
}

const files = walk(ROOT);
let changed = 0;
for (const f of files) {
  const before = fs.readFileSync(f, 'utf8');
  const after = rewrite(before);
  if (after !== before) {
    fs.writeFileSync(f, after);
    changed++;
    console.log(`rewrote ${path.relative(process.cwd(), f)}`);
  }
}
console.log(`\n${changed}/${files.length} files rewritten.`);
