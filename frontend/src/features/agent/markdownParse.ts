/**
 * Block-level Markdown parsing for agent replies (see markdown.tsx). Tolerant of partial input: replies
 * are re-parsed on every streamed chunk, and an unterminated ``` fence renders as code so far.
 */

export type TableAlign = "left" | "center" | "right" | undefined;

export type MdBlock =
  | { type: "p"; text: string }
  | { type: "h"; level: number; text: string }
  | { type: "ul"; items: string[] }
  | { type: "ol"; items: string[]; start: number }
  | { type: "quote"; text: string }
  | { type: "code"; lang: string; text: string; closed: boolean }
  | { type: "table"; headers: string[]; alignments: TableAlign[]; rows: string[][] }
  | { type: "hr" }
  /** A server notice line "[[notice:key]]", translated by the UI (agent.notice.<key>). */
  | { type: "notice"; key: string };

const reFence = /^\s*(```|~~~)\s*([\w+-]*)\s*$/;
const reHeading = /^(#{1,4})\s+(.*)$/;
const reUl = /^\s*[-*+]\s+(.*)$/;
const reOl = /^\s*(\d{1,4})[.)]\s+(.*)$/;
const reQuote = /^\s*>\s?(.*)$/;
const reNotice = /^\s*\[\[notice:([a-z_]+)\]\]\s*$/;
const reHr = /^\s*([-*_])(\s*\1){2,}\s*$/;

export function splitCells(line: string): string[] {
  let text = line.trim();
  if (text.startsWith("|")) text = text.slice(1);
  if (text.endsWith("|") && !text.endsWith("\\|")) text = text.slice(0, -1);
  const cells: string[] = [];
  let cur = "";
  let escaped = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (escaped) {
      cur += ch;
      escaped = false;
    } else if (ch === "\\") {
      escaped = true;
    } else if (ch === "|") {
      cells.push(cur.trim());
      cur = "";
    } else {
      cur += ch;
    }
  }
  cells.push(cur.trim());
  return cells;
}

export function isDelimiterRow(cells: string[]): boolean {
  if (cells.length === 0) return false;
  return cells.every((c) => /^:?-{1,}:?$/.test(c));
}

export function parseAlignment(cell: string): TableAlign {
  const trimmed = cell.trim();
  const left = trimmed.startsWith(":");
  const right = trimmed.endsWith(":");
  if (left && right) return "center";
  if (right) return "right";
  if (left) return "left";
  return undefined;
}

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
      let closed = false;
      while (i < lines.length) {
        if (reFence.test(lines[i])) {
          closed = true;
          break;
        }
        body.push(lines[i]);
        i++;
      }
      blocks.push({
        type: "code",
        lang: fence[2] ?? "",
        text: body.join("\n"),
        closed,
      });
      continue;
    }

    if (line.trim() === "") {
      flushPara();
      continue;
    }

    // Markdown Table check: header row with '|' followed by delimiter row with '---'
    if (line.includes("|") && i + 1 < lines.length && lines[i + 1].includes("|")) {
      const headerCells = splitCells(line);
      const delimCells = splitCells(lines[i + 1]);
      if (headerCells.length > 0 && isDelimiterRow(delimCells)) {
        flushPara();
        const alignments = delimCells.map(parseAlignment);
        while (alignments.length < headerCells.length) {
          alignments.push(undefined);
        }
        i += 2;
        const rows: string[][] = [];
        while (i < lines.length) {
          const rowLine = lines[i];
          if (rowLine.trim() === "" || !rowLine.includes("|") || reFence.test(rowLine)) {
            break;
          }
          const rowCells = splitCells(rowLine);
          while (rowCells.length < headerCells.length) {
            rowCells.push("");
          }
          rows.push(rowCells.slice(0, headerCells.length));
          i++;
        }
        i--;
        blocks.push({
          type: "table",
          headers: headerCells,
          alignments,
          rows,
        });
        continue;
      }
    }

    const notice = reNotice.exec(line);
    if (notice) {
      flushPara();
      blocks.push({ type: "notice", key: notice[1] });
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
