import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { translate, type Translate } from "../../lib/i18n";
import { Markdown } from "./markdown";
import { parseBlocks } from "./markdownParse";
import { bareToolName, toolLabel } from "./toolLabels";

const html = (text: string) => renderToStaticMarkup(createElement(Markdown, { text }));

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
      { type: "code", lang: "json", text: '{"a": 1' },
    ]);
  });

  it("keeps single line breaks inside a paragraph", () => {
    expect(parseBlocks("line one\nline two")).toEqual([{ type: "p", text: "line one\nline two" }]);
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
});

describe("toolLabel", () => {
  const vi: Translate = (key, vars) => translate("vi", key, vars);

  it("maps known tools, including provider-prefixed names", () => {
    expect(toolLabel(vi, "workspace_read_file")).toBe("Mở file");
    expect(toolLabel(vi, "mcp__dezuxk__workflow_run_node")).toBe("Chạy một tính năng");
    expect(bareToolName("dezuxk/workflow_list_specs")).toBe("workflow_list_specs");
  });

  it("prettifies unknown tools instead of failing", () => {
    expect(toolLabel(vi, "web_search")).toBe("Web search");
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
