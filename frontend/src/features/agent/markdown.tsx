import React from "react";
import { openExternal } from "../../lib/bridge";
import { parseBlocks } from "./markdownParse";

/**
 * Minimal, safe Markdown renderer for agent replies.
 *
 * Agents answer in Markdown (lists, bold, headings, tables of settings). Rendering it as plain text shows
 * raw `**` and `-` markers. This renderer builds React elements only — never HTML strings — so model
 * output cannot inject markup. It is tolerant of partial input, because replies are re-rendered on every
 * streamed chunk (an unterminated ``` fence simply renders as a code block so far).
 *
 * Supported: paragraphs (single line breaks kept), #–#### headings, - * + and 1. lists, > quotes, ---,
 * ``` fences, `code`, **bold**, *italic* / _italic_, ~~strike~~ and [links](https://…).
 */

// Inline tokens, earliest match wins: `code`, [text](url), **bold**, ~~strike~~, *italic* / _italic_.
const reInline =
  /(`+)([^`]+?)\1|\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)|\*\*(?=\S)([^*]+?)\*\*|~~(?=\S)([^~]+?)~~|(?<![\w*])\*(?=\S)([^*\n]+?)\*(?!\w)|(?<![\w_])_(?=\S)([^_\n]+?)_(?!\w)/g;

/** Renders inline Markdown into React nodes. */
function renderInline(text: string, keyPrefix = "i"): React.ReactNode[] {
  const out: React.ReactNode[] = [];
  let last = 0;
  let n = 0;
  for (const m of text.matchAll(reInline)) {
    const idx = m.index ?? 0;
    if (idx > last) out.push(text.slice(last, idx));
    const key = `${keyPrefix}-${n++}`;
    if (m[2] !== undefined) {
      out.push(
        <code key={key} className="md-code">
          {m[2]}
        </code>
      );
    } else if (m[3] !== undefined && m[4] !== undefined) {
      const url = m[4];
      out.push(
        <a
          key={key}
          href={url}
          className="md-link"
          title={url}
          onClick={(e) => {
            // Never navigate the app's own webview; hand https links to the system browser.
            e.preventDefault();
            openExternal(url);
          }}
        >
          {m[3]}
        </a>
      );
    } else if (m[5] !== undefined) {
      out.push(<strong key={key}>{renderInline(m[5], key)}</strong>);
    } else if (m[6] !== undefined) {
      out.push(<del key={key}>{m[6]}</del>);
    } else {
      out.push(<em key={key}>{m[7] ?? m[8]}</em>);
    }
    last = idx + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** Renders inline text, keeping single line breaks. */
function renderLines(text: string, key: string): React.ReactNode[] {
  const parts = text.split("\n");
  return parts.flatMap((line, i) => {
    const nodes = renderInline(line, `${key}-${i}`);
    return i < parts.length - 1 ? [...nodes, <br key={`${key}-br-${i}`} />] : nodes;
  });
}

export const Markdown: React.FC<{ text: string; className?: string }> = React.memo(
  ({ text, className }) => {
    const blocks = parseBlocks(text);
    return (
      <div className={`md${className ? ` ${className}` : ""}`}>
        {blocks.map((b, i) => {
          const key = `b${i}`;
          switch (b.type) {
            case "p":
              return <p key={key}>{renderLines(b.text, key)}</p>;
            case "h": {
              const Tag = `h${Math.min(b.level + 2, 6)}` as "h3" | "h4" | "h5" | "h6";
              return (
                <Tag key={key} className="md-heading">
                  {renderInline(b.text, key)}
                </Tag>
              );
            }
            case "ul":
              return (
                <ul key={key}>
                  {b.items.map((item, j) => (
                    <li key={j}>{renderLines(item, `${key}-${j}`)}</li>
                  ))}
                </ul>
              );
            case "ol":
              return (
                <ol key={key} start={b.start}>
                  {b.items.map((item, j) => (
                    <li key={j}>{renderLines(item, `${key}-${j}`)}</li>
                  ))}
                </ol>
              );
            case "quote":
              return <blockquote key={key}>{renderLines(b.text, key)}</blockquote>;
            case "code":
              return (
                <pre key={key} className="md-pre" data-lang={b.lang || undefined}>
                  <code>{b.text}</code>
                </pre>
              );
            case "hr":
              return <hr key={key} />;
          }
        })}
      </div>
    );
  }
);
Markdown.displayName = "Markdown";
