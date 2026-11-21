import { build } from 'esbuild';
import { createHash } from 'node:crypto';
import { readFile, mkdir, copyFile, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import path from 'node:path';

const upstream = JSON.parse(await readFile('vendor/cognee/upstream.json', 'utf8'));
for (const [name, hash] of Object.entries(upstream.files)) {
  const bytes = await readFile(path.join('vendor/cognee', name));
  if (createHash('sha256').update(bytes).digest('hex') !== hash) throw Error(`Official source changed: ${name}`);
}
await mkdir('dist', { recursive: true });
const result = await build({entryPoints: ['src/main.tsx'], outdir: 'dist', entryNames: 'app', bundle: true,
  minify: true, format: 'iife', target: 'safari17', jsx: 'automatic', metafile: true,
  alias: {'@': path.resolve('vendor/cognee/src')}, legalComments: 'linked',
  define: {'process.env.NODE_ENV': '"production"'}});
for (const name of Object.keys(result.metafile.inputs)) {
  if (name.startsWith('vendor/cognee/') && !upstream.files[name.slice('vendor/cognee/'.length)]) {
    throw Error(`Unpinned official source: ${name}`);
  }
}
const notices = new Map();
const missingLicenseTexts = [];
const supplements = JSON.parse(await readFile('licenses/manifest.json', 'utf8'));
for (const name of Object.keys(result.metafile.inputs).filter((name) => name.includes('node_modules/'))) {
  let directory = path.dirname(path.resolve(name));
  while (directory.includes('node_modules')) {
    try {
      const pkg = JSON.parse(await readFile(path.join(directory, 'package.json'), 'utf8'));
      if (pkg.name && pkg.version) {
        const key = `${pkg.name}@${pkg.version}`;
        if (!notices.has(key)) {
          let license = '';
          for (const filename of ['LICENSE', 'LICENSE.md', 'LICENSE.txt', 'license', 'license.md']) {
            try { license = await readFile(path.join(directory, filename), 'utf8'); break; } catch {}
          }
          if (!license && supplements[key]) {
            const extra = supplements[key];
            const bytes = await readFile(path.join('licenses', extra.file));
            if (createHash('sha256').update(bytes).digest('hex') !== extra.sha256) throw Error(`License digest mismatch: ${key}`);
            license = bytes.toString('utf8');
          }
          if (!license) {
            missingLicenseTexts.push(key);
            license = `The published package includes no license file. Declared license above; source repository: ${JSON.stringify(pkg.repository)}. Retained as an unresolved distribution item.`;
          }
          notices.set(key, `${key}\nDeclared license: ${pkg.license}\n${license}`);
        }
        break;
      }
    } catch (error) { if (error.message.startsWith('License digest mismatch')) throw error; }
    directory = path.dirname(directory);
  }
}
if (missingLicenseTexts.length > 0) {
  throw Error(`Missing distribution license texts: ${missingLicenseTexts.join(', ')}`);
}
const compactInputs = Object.fromEntries(
  Object.entries(result.metafile.inputs)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, meta]) => [name, { bytes: meta.bytes }]),
);
const compactOutputs = Object.fromEntries(
  Object.entries(result.metafile.outputs)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, meta]) => [name, { bytes: meta.bytes, ...(meta.entryPoint ? { entryPoint: meta.entryPoint } : {}) }]),
);
execFileSync('pnpm', ['exec', 'tailwindcss', '-i', 'src/host.css', '-o', 'dist/host.css', '--minify'], {stdio: 'inherit'});
await copyFile('index.html', 'dist/index.html');
await copyFile('vendor/cognee/LICENSE', 'dist/COGNEE-LICENSE');
await writeFile('dist/THIRD-PARTY-LICENSES.txt', [...notices.entries()].sort().map(([, text]) => text).join('\n\n'));
await writeFile('dist/build.json', JSON.stringify({upstream: upstream.commit, modifications: upstream.modifications,
  missingLicenseTexts, inputs: compactInputs, outputs: compactOutputs}, null, 2) + '\n');
const assetFiles = ['index.html','app.js','app.css','host.css','app.js.LEGAL.txt','COGNEE-LICENSE','THIRD-PARTY-LICENSES.txt','build.json'];
const assetHashes = {};
for (const name of assetFiles) assetHashes[name] = createHash('sha256').update(await readFile('dist/'+name)).digest('hex');
await writeFile('dist/assets.json',JSON.stringify({protocol:1,upstream:upstream.commit,files:assetHashes},null,2)+'\n');
