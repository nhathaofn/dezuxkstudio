// Style guard (run by `npm run lint`): keeps every stylesheet on the design tokens.
//   - frame thickness comes from --border-width, focus from --focus-width (one frame, never a second ring)
//   - no colour literals outside styles/tokens.css
//   - scrollbars are styled in ONE place (styles/scrollbar.css), the only exception being declared there
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const src = join(fileURLToPath(new URL(".", import.meta.url)), "..", "src");

const cssFiles = (dir) =>
  readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return cssFiles(p);
    return p.endsWith(".css") ? [p] : [];
  });

const files = cssFiles(src).map((p) => ({
  file: relative(src, p).replaceAll("\\", "/"),
  text: readFileSync(p, "utf8").replace(/\/\*[\s\S]*?\*\//g, ""),
}));

const declarations = (list) =>
  list.flatMap(({ file, text }) =>
    text
      .split(/[;{}]/)
      .map((d) => d.trim())
      .filter((d) => /^[a-z-]+\s*:/.test(d))
      .map((d) => ({ file, d }))
  );

const problems = [];
const report = (rule, items) => items.forEach((x) => problems.push(`${rule}: ${x.file}: ${x.d}`));

// A guard that parses nothing would pass for the wrong reason.
const all = declarations(files);
if (files.length < 10 || all.length < 500) {
  problems.push(`sanity: parsed only ${files.length} files / ${all.length} declarations`);
}

const nonToken = declarations(files.filter((f) => f.file !== "styles/tokens.css"));

report(
  "border width must use var(--border-width)",
  nonToken.filter(
    ({ d }) =>
      /^border(-top|-right|-bottom|-left)?\s*:\s*[0-9.]+px/.test(d) &&
      !/^border-(left|bottom)\s*:\s*2px/.test(d)
  )
);
report(
  "only state/quote indicators may be 2px (selected-row bar, tab underline, markdown quote bar)",
  nonToken.filter(
    ({ d }) =>
      /^border(-top|-right|-bottom|-left)?\s*:\s*2px/.test(d) &&
      !/^border-(left|bottom)\s*:\s*2px solid (transparent|var\(--border-default\))$/.test(d)
  )
);
report(
  "focus must be one --focus-width frame (no wide outline, no positive offset, no ring shadow)",
  nonToken.filter(
    ({ d }) =>
      /^outline\s*:\s*[2-9][0-9.]*px/.test(d) ||
      /^outline-offset\s*:\s*[0-9.]+px/.test(d) ||
      /^box-shadow\s*:\s*0 0 0 [0-9.]+px var\(--border-focus\)/.test(d)
  )
);
report(
  "colour literal outside tokens.css (use a token)",
  nonToken.filter(({ d }) => /#[0-9a-fA-F]{3,8}\b|\b(rgb|hsl)a?\(/.test(d) && !/^background-image\s*:/.test(d))
);
report(
  "scrollbar styling belongs in styles/scrollbar.css",
  nonToken.filter(
    ({ file, d }) =>
      file !== "styles/scrollbar.css" && /^scrollbar-(width|color|gutter)\s*:/.test(d) && !/scrollbar-gutter/.test(d)
  )
);
report(
  "::-webkit-scrollbar belongs in styles/scrollbar.css",
  files
    .filter((f) => f.file !== "styles/scrollbar.css" && /::-webkit-scrollbar/.test(f.text))
    .map((f) => ({ file: f.file, d: "::-webkit-scrollbar" }))
);

if (problems.length) {
  console.error(`check-styles: ${problems.length} problem(s)\n  ` + problems.join("\n  "));
  process.exit(1);
}
console.log(`check-styles: ok (${files.length} stylesheets, ${all.length} declarations)`);
