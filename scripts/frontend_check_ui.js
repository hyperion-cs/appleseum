import { readFileSync, readdirSync } from "node:fs";
import assert from "node:assert/strict";
const css = readFileSync(
  new URL("../frontend/src/style.css", import.meta.url),
  "utf8",
);
const components = new URL("../frontend/src/", import.meta.url);
for (const name of readdirSync(components, { recursive: true }).filter((name) =>
  name.endsWith(".vue"),
))
  assert(
    !/<select(?:\s|>)/i.test(readFileSync(new URL(name, components), "utf8")),
    `${name}: use ChoiceSelect instead of an OS dropdown`,
  );
const blocks = [
  ...css.matchAll(
    /:root(?:\[data-theme=(?:"dark"|'dark'|dark)\])?\s*\{([^}]+)\}/g,
  ),
]
  .map((m) =>
    Object.fromEntries(
      [...m[1].matchAll(/(--[\w-]+):([^;\n]+)/g)].map((p) => [
        p[1],
        p[2].trim(),
      ]),
    ),
  )
  .filter((theme) => Object.hasOwn(theme, "--bg"));
assert.equal(blocks.length, 2);
const darkKeys = Object.keys(blocks[1]);
for (const key of [
  "--bg",
  "--surface",
  "--text",
  "--muted",
  "--line",
  "--subtle-line",
  "--accent",
  "--primary",
  "--on-primary",
  "--soft",
  "--selected",
  "--danger",
  "--danger-bg",
  "--sharing",
  "--warning",
  "--warning-bg",
  "--success",
  "--success-bg",
  "--shadow",
  "--overlay",
])
  assert(darkKeys.includes(key), `dark token missing: ${key}`);

function luminance(hex) {
  const v = hex
    .slice(1)
    .match(/../g)
    .map((x) => parseInt(x, 16) / 255)
    .map((x) => (x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4));
  return v[0] * 0.2126 + v[1] * 0.7152 + v[2] * 0.0722;
}

function ratio(a, b) {
  const values = [luminance(a), luminance(b)].sort((a, b) => b - a);
  return (values[0] + 0.05) / (values[1] + 0.05);
}

for (const [i, theme] of blocks.entries()) {
  for (const foreground of [
    "--text",
    "--muted",
    "--accent",
    "--danger",
    "--sharing",
  ])
    for (const bg of ["--bg", "--surface", "--soft", "--selected"])
      assert(
        ratio(theme[foreground], theme[bg]) >= 4.5,
        `${i}: ${foreground} on ${bg}`,
      );
  assert(ratio(theme["--on-primary"], theme["--primary"]) >= 4.5);
  assert(ratio(theme["--warning"], theme["--warning-bg"]) >= 4.5);
  assert(ratio(theme["--success"], theme["--success-bg"]) >= 4.5);
  assert(ratio(theme["--danger"], theme["--danger-bg"]) >= 4.5);
  for (const bg of ["--bg", "--surface"])
    assert(ratio(theme["--line"], theme[bg]) >= 3, `${i}: control outline`);
}
const strings = new URL("../frontend/src/i18n/groups/", import.meta.url);
const source = readdirSync(strings)
  .map((name) => readFileSync(new URL(name, strings), "utf8"))
  .join("\n");
for (const banned of [
  /не просто/iu,
  /важно понимать/iu,
  /стоит отметить/iu,
  /в современном мире/iu,
  /таким образом/iu,
  /в конечном итоге/iu,
  /всё начинается/iu,
])
  assert(!banned.test(source), `banned UI wording: ${banned}`);
assert(
  !/v-html|innerHTML/.test(
    readFileSync(
      new URL("../frontend/src/app/App.vue", import.meta.url),
      "utf8",
    ),
  ),
);
console.log("Theme tokens, contrast and UI wording checks passed");
