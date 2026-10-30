#!/usr/bin/env python3
"""Generate site/project.html from docs/PROJECT.md (stdlib only, no network).

The page is a self-contained static HTML rendering that reuses site/styles.css.
Only plain href hyperlinks may point outside the site; no external resources
(scripts, fonts, images, CDN) are emitted.

Usage:
    python3 scripts/generate_project_page.py            # write site/project.html
    python3 scripts/generate_project_page.py --check    # exit 1 if output is stale
"""

import html
import posixpath
import re
import sys
from pathlib import Path
from urllib.parse import quote

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "docs" / "PROJECT.md"
OUT = ROOT / "site" / "project.html"
GH_BLOB = "https://github.com/paas-ops/kubectl-triage/blob/main/"
GH_REPO = "https://github.com/paas-ops/kubectl-triage"

BT = chr(96)
FENCE = BT * 3

HEADING_RE = re.compile(r"^(#{1,6})\s+(.*?)\s*#*\s*$")
FENCE_RE = re.compile(r"^" + FENCE + r"\s*([A-Za-z0-9_+-]*)\s*$")
LINK_RE = re.compile(r"\[([^\]]+)\]\(([^)\s]+)\)")
URL_RE = re.compile(r"https?://[^\s<>()\[\]" + BT + r"]+")
BULLET_RE = re.compile(r"^\s*[-*]\s+(.*)$")
ORDERED_RE = re.compile(r"^\s*(\d+)[.)]\s+(.*)$")
TABLE_SEP_CELL = re.compile(r"^:?-{3,}:?$")
CJK_RE = re.compile(r"[\u2e80-\u9fff\uf900-\ufaff\uff00-\uffef]")


def slugify(text):
    """GitHub-style heading slug for in-page anchors."""
    text = strip_markup(text).strip().lower()
    out = []
    for ch in text:
        if ch.isalnum() or ch in "-_":
            out.append(ch)
        elif ch == " ":
            out.append("-")
    slug = re.sub(r"-+", "-", "".join(out)).strip("-")
    return slug or "section"


def strip_markup(text):
    text = LINK_RE.sub(r"\1", text)
    text = text.replace("**", "").replace(BT, "")
    return text


def escape(text):
    return html.escape(text, quote=False)


def escape_attr(text):
    return html.escape(text, quote=True)


def rewrite_href(target):
    """Rewrite repository-relative Markdown/code links to GitHub blob URLs."""
    if target.startswith(("http://", "https://", "mailto:", "#")):
        return target
    file_part, sep, anchor = target.partition("#")
    if not file_part:
        return target
    if file_part.startswith("/"):
        repo_path = file_part.lstrip("/")
    else:
        repo_path = posixpath.normpath(posixpath.join("docs", file_part))
    if repo_path.startswith("../"):
        repo_path = repo_path[3:]
    url = GH_BLOB + quote(repo_path, safe="/")
    if sep:
        url += "#" + quote(anchor, safe="")
    return url


def render_inline(text):
    """Render inline Markdown: code, bold, links, bare URLs; escape everything else."""
    parts = []
    i = 0
    plain = []

    def flush_plain():
        if plain:
            parts.append(escape("".join(plain)))
            del plain[:]

    while i < len(text):
        ch = text[i]
        if ch == BT:
            j = text.find(BT, i + 1)
            if j != -1:
                flush_plain()
                parts.append("<code>" + escape(text[i + 1:j]) + "</code>")
                i = j + 1
                continue
        if text.startswith("**", i):
            j = text.find("**", i + 2)
            if j != -1:
                flush_plain()
                parts.append("<strong>" + render_inline(text[i + 2:j]) + "</strong>")
                i = j + 2
                continue
        if ch == "[":
            m = LINK_RE.match(text, i)
            if m:
                flush_plain()
                parts.append(
                    '<a href="' + escape_attr(rewrite_href(m.group(2))) + '">'
                    + render_inline(m.group(1)) + "</a>"
                )
                i = m.end()
                continue
        if ch == "h" and text.startswith(("http://", "https://"), i):
            m = URL_RE.match(text, i)
            if m:
                url = m.group(0).rstrip(".,;:!?")
                flush_plain()
                parts.append(
                    '<a href="' + escape_attr(url) + '">' + escape(url) + "</a>"
                )
                i += len(url)
                continue
        plain.append(ch)
        i += 1
    flush_plain()
    return "".join(parts)


def split_table_row(line):
    """Split a pipe-table row, ignoring pipes inside inline code spans."""
    text = line.strip()
    if text.startswith("|"):
        text = text[1:]
    if text.endswith("|"):
        text = text[:-1]
    cells = []
    buf = []
    in_code = False
    i = 0
    while i < len(text):
        ch = text[i]
        if ch == BT:
            in_code = not in_code
            buf.append(ch)
        elif ch == "|" and not in_code:
            cells.append("".join(buf).strip())
            buf = []
        else:
            buf.append(ch)
        i += 1
    cells.append("".join(buf).strip())
    return cells


def is_table_start(lines, i):
    if i + 1 >= len(lines):
        return False
    if "|" not in lines[i]:
        return False
    cells = split_table_row(lines[i + 1])
    if not cells:
        return False
    return all(TABLE_SEP_CELL.match(cell.replace(" ", "")) for cell in cells)


def is_cjk(text):
    return bool(CJK_RE.search(text))


def join_soft(lines):
    """Join soft-wrapped Markdown lines without inserting spaces between CJK text."""
    if not lines:
        return ""
    out = lines[0]
    for nxt in lines[1:]:
        if not out or not nxt:
            out += nxt
            continue
        a, b = out[-1], nxt[0]
        if is_cjk(a) or is_cjk(b) or a in "，。；：、）】》" or b in "，。；：、）】》":
            out += nxt
        else:
            out += " " + nxt
    return out


def starts_block(line):
    stripped = line.strip()
    if not stripped:
        return True
    return bool(
        FENCE_RE.match(stripped)
        or HEADING_RE.match(line)
        or stripped.startswith(">")
        or BULLET_RE.match(line)
        or ORDERED_RE.match(line)
        or stripped in ("---", "***", "___")
    )


def unique_anchor(base, used):
    slug = base
    counter = 1
    while slug in used:
        slug = base + "-" + str(counter)
        counter += 1
    used.add(slug)
    return slug


def render_blocks(lines):
    blocks = []
    used = set()
    i = 0
    while i < len(lines):
        line = lines[i]
        stripped = line.strip()
        if not stripped:
            i += 1
            continue

        m = FENCE_RE.match(stripped)
        if m:
            lang = m.group(1)
            i += 1
            code_lines = []
            while i < len(lines) and lines[i].strip() != FENCE:
                code_lines.append(lines[i])
                i += 1
            if i < len(lines):
                i += 1
            code = "\n".join(code_lines)
            cls = ' class="language-' + escape_attr(lang) + '"' if lang else ""
            blocks.append("<pre><code" + cls + ">" + escape(code) + "</code></pre>")
            continue

        m = HEADING_RE.match(line)
        if m:
            level = len(m.group(1))
            text = m.group(2).strip()
            anchor = unique_anchor(slugify(text), used)
            blocks.append(
                "<h" + str(level) + ' id="' + escape_attr(anchor) + '">'
                + render_inline(text) + "</h" + str(level) + ">"
            )
            i += 1
            continue

        if stripped.startswith(">"):
            quote_lines = []
            while i < len(lines) and lines[i].strip().startswith(">"):
                content = lines[i].strip()[1:]
                if content.startswith(" "):
                    content = content[1:]
                quote_lines.append(content)
                i += 1
            paragraphs = []
            current = []
            for qline in quote_lines:
                if not qline.strip():
                    if current:
                        paragraphs.append(current)
                        current = []
                    continue
                current.append(qline)
            if current:
                paragraphs.append(current)
            inner = ""
            for para in paragraphs:
                inner += "<p>" + render_inline(join_soft(para)) + "</p>"
            blocks.append("<blockquote>" + inner + "</blockquote>")
            continue

        if BULLET_RE.match(line) or ORDERED_RE.match(line):
            ordered = bool(ORDERED_RE.match(line))
            items = []
            while i < len(lines) and lines[i].strip():
                item = BULLET_RE.match(lines[i]) if not ordered else ORDERED_RE.match(lines[i])
                if not item:
                    # continuation line of the previous item
                    if items:
                        items[-1].append(lines[i].strip())
                        i += 1
                        continue
                    break
                items.append([item.group(item.lastindex)])
                i += 1
            tag = "ol" if ordered else "ul"
            inner = ""
            for item in items:
                inner += "<li>" + render_inline(join_soft(item)) + "</li>"
            blocks.append("<" + tag + ">" + inner + "</" + tag + ">")
            continue

        if is_table_start(lines, i):
            header = split_table_row(lines[i])
            i += 2
            rows = []
            while i < len(lines) and lines[i].strip() and "|" in lines[i]:
                rows.append(split_table_row(lines[i]))
                i += 1
            inner = "<thead><tr>"
            for cell in header:
                inner += "<th>" + render_inline(cell) + "</th>"
            inner += "</tr></thead><tbody>"
            for row in rows:
                inner += "<tr>"
                for cell in row:
                    inner += "<td>" + render_inline(cell) + "</td>"
                inner += "</tr>"
            inner += "</tbody>"
            blocks.append('<div class="table-wrap"><table>' + inner + "</table></div>")
            continue

        if stripped in ("---", "***", "___"):
            blocks.append("<hr>")
            i += 1
            continue

        para = [line]
        i += 1
        while i < len(lines) and not starts_block(lines[i]) and not is_table_start(lines, i):
            para.append(lines[i])
            i += 1
        blocks.append("<p>" + render_inline(join_soft(para)) + "</p>")

    return "\n".join(blocks)


def page_html(article):
    return """<!DOCTYPE html>
<!--
  Generated file: do not edit by hand.
  Source: docs/PROJECT.md
  Generator: scripts/generate_project_page.py
  Static by design: the only subresource is ./styles.css; external absolute
  links are documentation hyperlinks, never loaded resources.
-->
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>kubectl-triage 项目文档</title>
  <meta name="description" content="kubectl-triage 项目综合文档：定位、架构、数据契约、命令、安全、质量、里程碑与路线图（由 docs/PROJECT.md 生成）。">
  <meta name="color-scheme" content="light dark">
  <link rel="stylesheet" href="styles.css">
</head>
<body>
  <a class="skip-link" href="#main">跳到主要内容</a>

  <header class="site-header">
    <div class="container header-inner">
      <a class="brand" href="index.html" aria-label="返回 kubectl-triage 首页">
        <svg class="brand-mark" viewBox="0 0 24 24" width="22" height="22" aria-hidden="true" focusable="false">
          <rect x="2" y="3.5" width="20" height="17" rx="3.5" fill="none" stroke="currentColor" stroke-width="1.6"></rect>
          <path d="M6.2 9.2l2.3 2.3-2.3 2.3" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"></path>
          <path d="M11.4 14h5.2" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"></path>
        </svg>
        <span>kubectl-triage</span>
      </a>
      <nav class="site-nav" aria-label="主导航">
        <a href="index.html">返回首页</a>
        <a href="index.html#install">安装</a>
        <a href="index.html#security">安全</a>
        <a href="index.html#eval">评测</a>
        <a href="project.html" aria-current="page">项目文档</a>
        <a class="nav-github" href="https://github.com/paas-ops/kubectl-triage">GitHub</a>
      </nav>
    </div>
  </header>

  <main id="main" class="section">
    <div class="container project-doc">
      <article>
""" + article + """
      </article>
      <p class="project-generated-note">
        本页由 <code>scripts/generate_project_page.py</code> 从
        <a href="https://github.com/paas-ops/kubectl-triage/blob/main/docs/PROJECT.md">docs/PROJECT.md</a>
        生成（纯静态、无 JavaScript）；修改内容请更新源文档后重新运行生成脚本。
      </p>
    </div>
  </main>

  <footer class="site-footer">
    <div class="container footer-inner">
      <div class="footer-brand">
        <strong>kubectl-triage</strong>
        <span>Apache-2.0 · 项目文档</span>
      </div>
      <nav class="footer-nav" aria-label="页脚导航">
        <a href="index.html">文档首页</a>
        <a href="https://github.com/paas-ops/kubectl-triage">GitHub</a>
        <a href="https://github.com/paas-ops/kubectl-triage/blob/main/docs/PROJECT.md">PROJECT.md 源文档</a>
        <a href="https://github.com/paas-ops/kubectl-triage/blob/main/CHANGELOG.md">Changelog</a>
        <a href="https://github.com/paas-ops/kubectl-triage/blob/main/SECURITY.md">安全策略</a>
      </nav>
      <p class="footer-note">纯静态页面：无外部字体、脚本、CDN 或分析代码，可离线打开。</p>
    </div>
  </footer>
</body>
</html>
"""


def build():
    source = SRC.read_text(encoding="utf-8")
    article = render_blocks(source.splitlines())
    return page_html(article)


def main(argv):
    check = "--check" in argv
    try:
        output = build()
    except FileNotFoundError:
        sys.stderr.write("missing source document: " + str(SRC) + "\n")
        return 1
    if check:
        existing = OUT.read_text(encoding="utf-8") if OUT.exists() else ""
        if existing == output:
            print("up to date: site/project.html")
            return 0
        sys.stderr.write("site/project.html is stale; run python3 scripts/generate_project_page.py\n")
        return 1
    OUT.write_text(output, encoding="utf-8")
    headings = len(re.findall(r"<h[1-6] id=", output))
    print("generated " + str(OUT.relative_to(ROOT)) + " (%d bytes, %d headings)" % (len(output.encode("utf-8")), headings))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

