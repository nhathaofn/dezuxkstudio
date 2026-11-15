import React, { useMemo } from "react";
import hljs from "highlight.js/lib/core";
import go from "highlight.js/lib/languages/go";
import typescript from "highlight.js/lib/languages/typescript";
import javascript from "highlight.js/lib/languages/javascript";
import python from "highlight.js/lib/languages/python";
import bash from "highlight.js/lib/languages/bash";
import json from "highlight.js/lib/languages/json";
import yaml from "highlight.js/lib/languages/yaml";
import sql from "highlight.js/lib/languages/sql";
import css from "highlight.js/lib/languages/css";
import xml from "highlight.js/lib/languages/xml";
import markdown from "highlight.js/lib/languages/markdown";
import diff from "highlight.js/lib/languages/diff";
import { Check, Copy } from "lucide-react";
import { useFlash } from "../../lib/hooks/useFlash";
import { appConfig } from "../../config/appConfig";
import { useT } from "../../lib/i18n";

// Register the 12 core languages explicitly (do NOT import full bundle)
hljs.registerLanguage("go", go);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("python", python);
hljs.registerLanguage("bash", bash);
hljs.registerLanguage("json", json);
hljs.registerLanguage("yaml", yaml);
hljs.registerLanguage("sql", sql);
hljs.registerLanguage("css", css);
hljs.registerLanguage("xml", xml);
hljs.registerLanguage("markdown", markdown);
hljs.registerLanguage("diff", diff);

export interface CodeBlockProps {
  code?: string;
  text?: string;
  lang?: string;
  closed?: boolean;
  className?: string;
}

function isLanguageSupported(lang: string): boolean {
  if (!lang) return false;
  return Boolean(hljs.getLanguage(lang.trim().toLowerCase()));
}

/**
 * Syntax-highlighted code block with streaming optimization, copy feedback,
 * and zero layout jump fallback to plain text.
 */
export const CodeBlock: React.FC<CodeBlockProps> = React.memo(
  ({ code, text, lang = "", closed = true, className }) => {
    const rawCode = code ?? text ?? "";
    const trimmedLang = lang.trim().toLowerCase();
    const t = useT();
    const [copied, flashCopied] = useFlash(appConfig.ui.codeBlock.copyFeedbackMs);

    const isSupported = isLanguageSupported(trimmedLang);
    const exceedsMax = rawCode.length > appConfig.ui.codeBlock.maxPlainCharacters;
    const canHighlight = closed && isSupported && !exceedsMax;

    const highlightedHtml = useMemo(() => {
      if (!canHighlight) return null;
      try {
        return hljs.highlight(rawCode, { language: trimmedLang, ignoreIllegals: true }).value;
      } catch {
        return null;
      }
    }, [rawCode, trimmedLang, canHighlight]);

    const handleCopy = async () => {
      try {
        if (typeof navigator !== "undefined" && navigator.clipboard) {
          await navigator.clipboard.writeText(rawCode);
          flashCopied(true);
        }
      } catch {
        // Clipboard is unavailable outside a secure context; nothing to do.
      }
    };

    const copyLabel = copied ? t("agent.chat.copied") : t("agent.login.copyCode");

    return (
      <div className={`md-codeblock${className ? ` ${className}` : ""}`}>
        <div className="md-codeblock__header">
          <span className="md-codeblock__lang">{trimmedLang || "text"}</span>
          <button
            type="button"
            className="md-codeblock__copy"
            onClick={() => void handleCopy()}
            title={copyLabel}
            aria-label={copyLabel}
          >
            {copied ? <Check size={12} /> : <Copy size={12} />}
            <span>{copyLabel}</span>
          </button>
        </div>
        <pre className="md-pre md-codeblock__pre" data-lang={trimmedLang || undefined}>
          {highlightedHtml !== null ? (
            <code
              className={`hljs language-${trimmedLang}`}
              dangerouslySetInnerHTML={{ __html: highlightedHtml }}
            />
          ) : (
            <code>{rawCode}</code>
          )}
        </pre>
      </div>
    );
  }
);

CodeBlock.displayName = "CodeBlock";
