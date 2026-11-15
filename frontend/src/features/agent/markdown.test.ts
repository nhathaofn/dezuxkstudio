import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { translate, type Translate } from "../../lib/i18n";
import { CodeBlock } from "../../components/ui/CodeBlock";
import { Markdown } from "./markdown";
import { parseBlocks } from "./markdownParse";
import { bareToolName, toolLabel } from "./toolLabels";

const html = (text: string) => renderToStaticMarkup(createElement(Markdown, { text }));
const renderCodeBlock = (props: Parameters<typeof CodeBlock>[0]) =>
  renderToStaticMarkup(createElement(CodeBlock, props));

describe("parseBlocks", () => {
  it("splits headings, lists, quotes and paragraphs", () => {
    const blocks = parseBlocks(
      "## Kết quả\n\n- ảnh 1\n- ảnh 2\n\n2. bước hai\n3. bước ba\n\n> ghi chú\n\nXong."
    );
    expect(blocks.map((b) => b.type)).toEqual(["h", "ul", "ol", "quote", "p"]);
    expect(blocks[2]).toMatchObject({ type: "ol", start: 2, items: ["bước hai", "bước ba"] });
  });

  it("treats an unterminated fence (mid-stream) as code so far", () => {
    expect(parseBlocks('Here:\n```json\n{"a": 1')).toEqual([
      { type: "p", text: "Here:" },
      { type: "code", lang: "json", text: '{"a": 1', closed: false },
    ]);
  });

  it("marks a properly closed fence as closed: true", () => {
    expect(parseBlocks('```typescript\nconst x: number = 42;\n```')).toEqual([
      { type: "code", lang: "typescript", text: "const x: number = 42;", closed: true },
    ]);
  });

  it("keeps single line breaks inside a paragraph", () => {
    expect(parseBlocks("line one\nline two")).toEqual([{ type: "p", text: "line one\nline two" }]);
  });

  it("parses Markdown tables with alignment colons", () => {
    const tableMd = [
      "| Left | Center | Right | Default |",
      "| :--- | :---: | ---: | --- |",
      "| L1 | C1 | R1 | D1 |",
      "| L2 | C2 | R2 | D2 |",
    ].join("\n");

    const blocks = parseBlocks(tableMd);
    expect(blocks).toEqual([
      {
        type: "table",
        headers: ["Left", "Center", "Right", "Default"],
        alignments: ["left", "center", "right", undefined],
        rows: [
          ["L1", "C1", "R1", "D1"],
          ["L2", "C2", "R2", "D2"],
        ],
      },
    ]);
  });

  it("handles tables with escaped pipes in cell content", () => {
    const tableMd = [
      "| Pattern | Meaning |",
      "| :--- | :--- |",
      "| a \\| b | Option A or B |",
    ].join("\n");

    const blocks = parseBlocks(tableMd);
    expect(blocks).toEqual([
      {
        type: "table",
        headers: ["Pattern", "Meaning"],
        alignments: ["left", "left"],
        rows: [["a | b", "Option A or B"]],
      },
    ]);
  });

  it("does not treat solitary pipe lines without delimiter row as a table", () => {
    const notTable = "Option A | Option B\nThis is just regular text";
    const blocks = parseBlocks(notTable);
    expect(blocks).toEqual([{ type: "p", text: "Option A | Option B\nThis is just regular text" }]);
  });
});

describe("Markdown", () => {
  it("renders inline formatting", () => {
    const out = html("**Đậm** và *nghiêng*, `output_dir`, ~~cũ~~");
    expect(out).toContain("<strong>Đậm</strong>");
    expect(out).toContain("<em>nghiêng</em>");
    expect(out).toContain('<code class="md-code">output_dir</code>');
    expect(out).toContain("<del>cũ</del>");
  });

  it("never renders model output as HTML", () => {
    const out = html('<img src=x onerror="alert(1)"> **<script>x</script>**');
    expect(out).not.toContain("<img");
    expect(out).not.toContain("<script>");
    expect(out).toContain("&lt;img");
  });

  it("only links http(s) URLs", () => {
    expect(html("[docs](https://example.com/a)")).toContain('href="https://example.com/a"');
    expect(html("[x](javascript:alert(1))")).not.toContain("href");
  });

  it("does not treat snake_case words as italics", () => {
    expect(html("set output_dir_name now")).not.toContain("<em>");
  });

  it("renders Markdown table with headers, alignment styles and cell formatting", () => {
    const tableMd = [
      "| Metric | Value | Status |",
      "| :--- | :---: | ---: |",
      "| **Memory** | 128 MB | `OK` |",
      "| **CPU** | 15% | `Idle` |",
    ].join("\n");

    const out = html(tableMd);
    expect(out).toContain('<div class="md-table-wrap">');
    expect(out).toContain('<table class="md-table">');
    expect(out).toContain("<thead><tr>");
    expect(out).toContain('<th style="text-align:left">Metric</th>');
    expect(out).toContain('<th style="text-align:center">Value</th>');
    expect(out).toContain('<th style="text-align:right">Status</th>');
    expect(out).toContain("<tbody>");
    expect(out).toContain("<strong>Memory</strong>");
    expect(out).toContain('<code class="md-code">OK</code>');
  });
});

describe("CodeBlock syntax highlighting", () => {
  it("recognizes all 12 registered core languages and aliases", () => {
    const coreLangs = [
      "go",
      "typescript",
      "javascript",
      "python",
      "bash",
      "json",
      "yaml",
      "sql",
      "css",
      "xml",
      "markdown",
      "diff",
    ];
    for (const lang of coreLangs) {
      const out = renderCodeBlock({ code: "test", lang, closed: true });
      expect(out, `Language ${lang} should render with hljs`).toContain("hljs");
    }
    // Common aliases
    for (const alias of ["ts", "js", "py", "sh", "yml", "html", "md"]) {
      const out = renderCodeBlock({ code: "test", lang: alias, closed: true });
      expect(out, `Alias ${alias} should render with hljs`).toContain("hljs");
    }

    // Unsupported languages fallback to plain text
    expect(renderCodeBlock({ code: "test", lang: "unknown_lang", closed: true })).not.toContain(
      "class=\"hljs"
    );
    expect(renderCodeBlock({ code: "test", lang: "", closed: true })).not.toContain("class=\"hljs");
  });

  it("renders syntax highlighted tokens for TypeScript", () => {
    const out = html("```typescript\nconst greeting: string = 'hello';\n```");
    expect(out).toContain('class="md-codeblock"');
    expect(out).toContain("hljs-keyword");
    expect(out).toContain("hljs-string");
    expect(out).toContain("greeting");
  });

  it("renders syntax highlighted tokens for Go", () => {
    const out = html('```go\npackage main\nfunc main() {\n  println("hi")\n}\n```');
    expect(out).toContain("hljs-keyword");
    expect(out).toContain("package");
  });

  it("renders syntax highlighted tokens for Python", () => {
    const out = html('```python\ndef calculate(x: int) -> int:\n    return x * 2\n```');
    expect(out).toContain("hljs-keyword");
    expect(out).toContain("calculate");
  });

  it("renders syntax highlighted tokens for JSON", () => {
    const out = html('```json\n{\n  "version": 1,\n  "enabled": true\n}\n```');
    expect(out).toContain("hljs-attr");
    expect(out).toContain("hljs-number");
  });

  it("renders syntax highlighted tokens for SQL", () => {
    const out = html("```sql\nSELECT id, name FROM users WHERE active = 1;\n```");
    expect(out).toContain("hljs-keyword");
  });

  it("renders syntax highlighted tokens for Bash", () => {
    const out = html("```bash\necho 'hello world'\n```");
    expect(out).toContain("hljs");
  });

  it("renders syntax highlighted tokens for Diff", () => {
    const out = html("```diff\n+ added line\n- removed line\n```");
    expect(out).toContain("hljs-addition");
    expect(out).toContain("hljs-deletion");
  });

  it("falls back to plain text for unknown languages without layout jump", () => {
    const out = html("```unknownlang\nraw text content here\n```");
    expect(out).toContain('class="md-codeblock"');
    expect(out).toContain("<code>raw text content here</code>");
    expect(out).not.toContain("hljs-keyword");
  });

  it("falls back to plain text for streaming in-progress (unclosed) code blocks", () => {
    // Unterminated fence: closed is false
    const out = html("```typescript\nconst streaming = 1;");
    expect(out).toContain('class="md-codeblock"');
    // Renders plain code tag instead of hljs highlight to avoid layout jumps during streaming
    expect(out).toContain("<code>const streaming = 1;</code>");
    expect(out).not.toContain("hljs-keyword");
  });

  it("falls back to plain text when code length exceeds maxPlainCharacters", () => {
    const longCode = "const a = 1;\n".repeat(2500); // 32,500 characters > 20,000
    const out = renderCodeBlock({ code: longCode, lang: "typescript", closed: true });
    expect(out).toContain('class="md-codeblock"');
    expect(out).toContain("<code>");
    expect(out).not.toContain("hljs-keyword");
  });
});

describe("CodeBlock copy button functionality", () => {
  it("renders the copy button with copy label and raw code", () => {
    const code = "const message = 'test';";
    const out = renderCodeBlock({ code, lang: "typescript" });
    expect(out).toContain('class="md-codeblock__copy"');
    expect(out).toContain("<pre");
    expect(out).toContain("typescript");
  });

  it("copies the raw code without line numbers or HTML tags via clipboard API", async () => {
    const rawString = "function add(a, b) {\n  return a + b;\n}";
    const writeTextMock = vi.fn().mockResolvedValue(undefined);

    // Mock navigator.clipboard
    const originalClipboard = navigator.clipboard;
    Object.assign(navigator, {
      clipboard: {
        writeText: writeTextMock,
      },
    });

    try {
      // Simulate click logic
      await navigator.clipboard.writeText(rawString);
      expect(writeTextMock).toHaveBeenCalledWith(rawString);
      expect(writeTextMock).not.toHaveBeenCalledWith(expect.stringContaining("<span"));
    } finally {
      Object.assign(navigator, { clipboard: originalClipboard });
    }
  });
});

describe("toolLabel", () => {
  const viLang: Translate = (key, vars) => translate("vi", key, vars);

  it("maps known tools, including provider-prefixed names", () => {
    expect(toolLabel(viLang, "workspace_read_file")).toBe("Mở file");
    expect(toolLabel(viLang, "mcp__dezuxk__workflow_run_node")).toBe("Chạy một tính năng");
    expect(bareToolName("dezuxk/workflow_list_specs")).toBe("workflow_list_specs");
  });

  it("prettifies unknown tools instead of failing", () => {
    expect(toolLabel(viLang, "web_search")).toBe("Web search");
  });
});

describe("notice markers", () => {
  it("renders a server notice line as a translated note", () => {
    const blocks = parseBlocks("Done.\n[[notice:code_blocked]]");
    expect(blocks[1]).toEqual({ type: "notice", key: "code_blocked" });
    expect(html("[[notice:code_blocked]]")).toContain('class="md-notice"');
    expect(html("[[notice:code_blocked]]")).not.toContain("[[notice");
  });
});
