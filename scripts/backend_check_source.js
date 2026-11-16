import { readFileSync, readdirSync } from "node:fs";
import { join, relative, dirname, sep } from "node:path";
import { fileURLToPath } from "node:url";
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const excluded = new Set([
  ".git",
  "node_modules",
  "build",
  "data",
  "out",
  "dist",
  ".vite",
  "coverage",
  "test-results",
  "playwright-report",
]);
const pythonNames = new Set([
  "pyproject.toml",
  "pipfile",
  "pipfile.lock",
  "poetry.lock",
  "uv.lock",
  "setup.cfg",
  "__pycache__",
]);

function check(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (excluded.has(entry.name)) continue;
    const path = join(dir, entry.name);
    const name = relative(root, path).split(sep).join("/");
    if (
      /\.(?:py[ciowd]?|ipynb)$/i.test(entry.name) ||
      /^requirements(?:[-.][^/]*)?\.txt$/i.test(entry.name) ||
      pythonNames.has(entry.name.toLowerCase())
    )
      throw new Error(`Python does not belong in public source: ${name}`);
    if (entry.isDirectory()) {
      check(path);
      continue;
    }
    const source = readFileSync(path, "utf8");
    if (/^#![^\r\n]*\bpython(?:\d+(?:\.\d+)*)?\b/i.test(source))
      throw new Error(`Python does not belong in public source: ${name}`);
    const runtime = !entry.name.endsWith("_test.go");
    if (
      runtime &&
      ((name.startsWith("backend/") && entry.name.endsWith(".go")) ||
        (name.startsWith("frontend/src/") &&
          /\.(?:ts|vue)$/.test(entry.name))) &&
      /howett\.net\/plist|hosting\.(?:Manifest|InstallURL|ManifestItem)\b|applekit\.(?:NewHttpClient|DefaultUserAgent|UserAgent)\b|itms-services:|[.]nip[.]io|CFBundle[A-Za-z]+|iTunesArtwork|CgBI|[.]mzstatic[.]com|(?:[a-z0-9-]+[.])+apple[.]com/i.test(
        source,
      )
    )
      throw new Error(
        `Apple protocols and formats belong in applekit: ${name}`,
      );
    if (
      !entry.name.endsWith(".go") ||
      entry.name.endsWith("_test.go") ||
      !(
        name.startsWith("applekit/") ||
        name.startsWith("backend/internal/artwork/")
      )
    )
      continue;
    if (
      /http\.Default(?:Client|Transport)|\bhttp\.(?:Get|Post|Head|PostForm)\(/.test(
        source,
      )
    )
      throw new Error(
        `Apple requests must use the shared uTLS client: ${name}`,
      );
    if (name.startsWith("applekit/internal/httpx/")) continue;
    for (const match of source.matchAll(/&http\.Client\{([^}]*)\}/g)) {
      if (!/\bTransport\s*:/.test(match[1]))
        throw new Error(`Missing shared Apple transport: ${name}`);
    }
  }
}

check(root);
process.stdout.write("Public source and Apple network client checks passed\n");
