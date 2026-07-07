// Copies generated developer docs from ../docs/developer into
// src/content/docs/docs/, adding Starlight frontmatter and rewriting
// relative .md links into site routes. Run automatically before dev/build
// so the docs site never drifts from `go run ./cmd/neo-docs` output.
import { mkdir, readdir, readFile, rm, writeFile } from 'node:fs/promises';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const websiteDir = resolve(scriptsDir, '..');
const repoRoot = resolve(websiteDir, '..');
const sourceDir = resolve(repoRoot, 'docs/developer');
// Nested under docs/reference (not docs/ directly) so this rm+regenerate
// step never clobbers the hand-written pages living beside it in docs/.
const destDir = resolve(websiteDir, 'src/content/docs/docs/reference');

await rm(destDir, { recursive: true, force: true });
await mkdir(destDir, { recursive: true });
await copyDir(sourceDir, destDir);

async function copyDir(source, destination) {
  await mkdir(destination, { recursive: true });
  for (const entry of await readdir(source, { withFileTypes: true })) {
    const sourcePath = join(source, entry.name);
    const destPath = join(destination, entry.name);
    if (entry.isDirectory()) {
      await copyDir(sourcePath, destPath);
      continue;
    }
    if (!entry.name.endsWith('.md')) continue;
    const raw = await readFile(sourcePath, 'utf8');
    const relPath = relative(sourceDir, sourcePath);
    await writeFile(destPath, transform(raw, relPath), 'utf8');
  }
}

function transform(raw, relPath) {
  const body = raw.replace(/^<!--.*?-->\n+/, '');
  const titleMatch = body.match(/^#\s+(.+)$/m);
  const title = titleMatch ? titleMatch[1].trim() : 'Untitled';
  const withoutTitle = titleMatch ? body.replace(titleMatch[0], '').trimStart() : body;
  const descMatch = withoutTitle.match(/^([^\n#].+)$/m);
  const description = (descMatch ? descMatch[1] : title).replace(/`/g, '').slice(0, 140);
  const rewritten = rewriteLinks(withoutTitle, relPath);
  const frontmatter = [
    '---',
    `title: ${yamlString(title)}`,
    `description: ${yamlString(description)}`,
    'editUrl: false',
    '---',
    '',
  ].join('\n');
  return frontmatter + rewritten;
}

function rewriteLinks(content, relPath) {
  const currentDir = dirname(relPath);
  return content.replace(/\]\(([^)]+?\.md)(#[^)]*)?\)/g, (_match, link, anchor = '') => {
    const targetRel = join(currentDir, link);
    const slug = toSlug(targetRel);
    return `](/docs/reference/${slug}/${anchor})`;
  });
}

function toSlug(relPath) {
  const noExt = relPath.replace(/\.md$/, '');
  return noExt === 'index' || noExt.endsWith('/index') ? noExt.replace(/\/?index$/, '') : noExt;
}

function yamlString(value) {
  return JSON.stringify(value);
}
