import { productName, productVersion } from "../frontend/src/api/types.ts";
import { routePaths, routeRedirects } from "../frontend/src/app/routes.ts";
import { spawnSync } from "node:child_process";
import {
  readFileSync,
  writeFileSync,
  readdirSync,
  mkdirSync,
  rmSync,
  statSync,
} from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync, brotliCompressSync, constants } from "node:zlib";
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const frontendOnly =
  process.argv.length === 3 && process.argv[2] === "--frontend-only";
if (process.argv.length > 2 && !frontendOnly) {
  throw new Error("Usage: node scripts/backend_build.js [--frontend-only]");
}
const version = process.env.APPLESEUM_VERSION ?? productVersion;
if (
  !/^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$/.test(
    version,
  )
) {
  throw new Error(`Invalid APPLESEUM_VERSION: ${version}`);
}

// The commit the frontend was built from, abbreviated as GitHub does, with "-x"
// when the working tree had uncommitted changes. A tree without git history,
// such as a container build, has none unless APPLESEUM_COMMIT supplies it,
// as a full or abbreviated hash.
function gitCommit() {
  const git = (...args) =>
    spawnSync("git", args, { cwd: root, encoding: "utf8" });
  const head = git("rev-parse", "HEAD");
  if (head.status !== 0) return "";
  const changed = git("status", "--porcelain").stdout.trim() !== "";
  return head.stdout.trim() + (changed ? "-x" : "");
}

const source = process.env.APPLESEUM_COMMIT ?? gitCommit();
const parts = /^(?:([0-9a-f]{7,40})(-x)?)?$/.exec(source);
if (!parts) {
  throw new Error(`Invalid APPLESEUM_COMMIT: ${source}`);
}
const commit = parts[1] ? parts[1].slice(0, 7) + (parts[2] ?? "") : "";
const entryPages = new Set(
  [...Object.values(routePaths), ...Object.keys(routeRedirects)].map((path) =>
    path.slice(1),
  ),
);
class CommandError extends Error {
  constructor(status) {
    super(`command failed with status ${status}`);
    this.status = status;
  }
}

function run(cmd, args, cwd = root, env = {}) {
  const result = spawnSync(cmd, args, {
    cwd,
    stdio: "inherit",
    env: { ...process.env, CGO_ENABLED: "0", ...env },
    shell: process.platform === "win32" && cmd === "npm",
  });
  if (result.status !== 0) throw new CommandError(result.status ?? 1);
}

function compress(dir) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      compress(path);
      continue;
    }
    if (
      !/\.(html|css|js|svg)$/.test(path) &&
      !(dir === dist && entryPages.has(name))
    )
      continue;
    const data = readFileSync(path);
    writeFileSync(path + ".gz", gzipSync(data, { level: 9 }));
    writeFileSync(
      path + ".br",
      brotliCompressSync(data, {
        params: { [constants.BROTLI_PARAM_QUALITY]: 11 },
      }),
    );
  }
}

const dist = join(root, "frontend", "dist");
let keepFrontend = false;
try {
  run(process.execPath, [join(root, "scripts", "backend_check_source.js")]);
  const host = spawnSync("go", ["env", "GOHOSTOS", "GOHOSTARCH"], {
    encoding: "utf8",
  });
  if (host.status !== 0) throw new CommandError(host.status ?? 1);
  const [GOOS, GOARCH] = host.stdout.trim().split(/\s+/);
  run(
    "go",
    ["test", "./scripts", "-run", "^TestSourceConventions$", "-count=1"],
    root,
    {
      GOOS,
      GOARCH,
    },
  );
  run(process.execPath, [
    "--test",
    join(root, "frontend", "tests", "source.unit.ts"),
  ]);
  // The generator runs locally even when the final binary targets another OS.
  run("go", ["run", "./backend/cmd/api-types", "--check"], root, {
    GOOS,
    GOARCH,
  });
  run("npm", ["run", "build"], join(root, "frontend"), {
    VITE_APP_VERSION: version,
    VITE_APP_COMMIT: commit,
  });
  compress(dist);

  if (frontendOnly) {
    keepFrontend = true;
  } else {
    const build = join(root, "build");
    mkdirSync(build, { recursive: true });
    const windows =
      (process.env.GOOS ?? process.platform) === "windows" ||
      (!process.env.GOOS && process.platform === "win32");
    run("go", [
      "build",
      "-trimpath",
      `-ldflags=-s -w -X appleseum/backend/internal/product.Version=${version}`,
      "-o",
      join(build, productName + (windows ? ".exe" : "")),
      "./backend/cmd/appleseum",
    ]);
  }
} catch (error) {
  if (error instanceof CommandError) process.exitCode = error.status;
  else throw error;
} finally {
  if (!keepFrontend) rmSync(dist, { recursive: true, force: true });
}
