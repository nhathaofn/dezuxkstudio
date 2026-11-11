/**
 * Block-level Markdown parsing for agent replies (see markdown.tsx). Tolerant of partial input: replies
 * are re-parsed on every streamed chunk, and an unterminated ``` fence renders as code so far.
 */

export type MdBlock =
  | { type: "p"; text: string }
  | { type: "h"; level: number; text: string }
  | { type: "ul"; items: string[] }
  | { type: "ol"; items: string[]; start: number }
  | { type: "quote"; text: string }
  | { type: "code"; lang: string; text: string }
  | { type: "hr" };

const reFence = /^\s*(```|~~~)\s*([\w+-]*)\s*$/;
const reHeading = /^(#{1,4})\s+(.*)$/;
const reUl = /^\s*[-*+]\s+(.*)$/;
const reOl = /^\s*(\d{1,4})[.)]\s+(.*)$/;
const reQuote = /^\s*>\s?(.*)$/;
const reHr = /^\s*([-*_])(\s*\1){2,}\s*$/;

/** Splits Markdown source into blocks. */
export function parseBlocks(src: string): MdBlock[] {
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  const blocks: MdBlock[] = [];
  let para: string[] = [];

  const flushPara = () => {
    if (para.length > 0) {
      blocks.push({ type: "p", text: para.join("\n") });
      para = [];
    }
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    const fence = reFence.exec(line);
    if (fence) {
      flushPara();
      const body: string[] = [];
      i++;
      while (i < lines.length && !reFence.test(lines[i])) {
        body.push(lines[i]);
        i++;
      }
      blocks.push({ type: "code", lang: fence[2] ?? "", text: body.join("\n") });
      continue;
    }

    if (line.trim() === "") {
      flushPara();
      continue;
    }

    if (reHr.test(line)) {
      flushPara();
      blocks.push({ type: "hr" });
      continue;
    }

    const heading = reHeading.exec(line);
    if (heading) {
      flushPara();
      blocks.push({ type: "h", level: heading[1].length, text: heading[2].trim() });
      continue;
    }

    const ul = reUl.exec(line);
    const ol = reOl.exec(line);
    if (ul || ol) {
      flushPara();
      const ordered = !ul;
      const items: string[] = [];
      const start = ol ? Number(ol[1]) : 1;
      while (i < lines.length) {
        const m = ordered ? reOl.exec(lines[i]) : reUl.exec(lines[i]);
        if (m) {
          items.push((ordered ? m[2] : m[1]).trim());
          i++;
          continue;
        }
        // An indented, non-empty line continues the previous item.
        if (items.length > 0 && /^\s{2,}\S/.test(lines[i])) {
          items[items.length - 1] += "\n" + lines[i].trim();
          i++;
          continue;
        }
        break;
      }
      i--;
      blocks.push(ordered ? { type: "ol", items, start } : { type: "ul", items });
      continue;
    }

    const quote = reQuote.exec(line);
    if (quote) {
      flushPara();
      const body: string[] = [];
      while (i < lines.length) {
        const m = reQuote.exec(lines[i]);
        if (!m) break;
        body.push(m[1]);
        i++;
      }
      i--;
      blocks.push({ type: "quote", text: body.join("\n") });
      continue;
    }

    para.push(line);
  }
  flushPara();
  return blocks;
}
