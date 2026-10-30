#!/usr/bin/env python3
"""Render docs/PROJECT.md (English) and docs/PROJECT-zh.md (Chinese) into Word.

Both documents share the same visual system (cover, header/footer, styles,
tables, code blocks) but use language-appropriate cover/header/footer text and
fonts. The script also validates the generated files (paragraphs, tables,
headings) and, in the default two-document mode, cross-checks that the English
and Chinese structures match.

Usage:
    python3 scripts/generate_project_docx.py                  # both default documents
    python3 scripts/generate_project_docx.py --lang en        # English default only
    python3 scripts/generate_project_docx.py --lang zh        # Chinese default only
    python3 scripts/generate_project_docx.py \
        --source docs/PROJECT.md --output /tmp/custom.docx --lang en
"""

import argparse
import sys
from pathlib import Path

from docx import Document
from docx.enum.text import WD_ALIGN_PARAGRAPH, WD_BREAK
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.shared import Cm, Pt, RGBColor

ROOT = Path(__file__).resolve().parent.parent
BT = chr(96)
FENCE = BT * 3
MONO = "Menlo"
HEAD = RGBColor(11, 61, 145)
MUTED = RGBColor(89, 89, 89)
CODE = RGBColor(36, 41, 46)
BLACK = RGBColor(31, 42, 68)

# Per-language rendering configuration. BODY_FONT/EAST_ASIA_FONT are applied by
# build_styles/set_font when a document is built.
LANGS = {
    "en": {
        "source": ROOT / "docs" / "PROJECT.md",
        "output": ROOT / "docs" / "exports" / "kubectl-triage-project-v0.5.0.docx",
        "title": "kubectl-triage",
        "subtitle": "Project Documentation",
        "tagline": "Lightweight, offline-first Kubernetes incident triage CLI",
        "meta": "Version v0.5.0   ·   2026-10-30   ·   Apache-2.0",
        "cover_note": "This document is rendered from docs/PROJECT.md in the repository; detailed rules follow the linked ADRs and documents.",
        "header": "kubectl-triage Project Documentation · v0.5.0",
        "footer_prefix": "kubectl-triage · Apache-2.0 · Page ",
        "footer_suffix": "",
        "body_font": "Helvetica Neue",
        "east_asia_font": "PingFang SC",
        "doc_title": "kubectl-triage Project Documentation",
        "doc_subject": "kubectl-triage v0.5.0 project documentation",
        "doc_comments": "Rendered from docs/PROJECT.md",
    },
    "zh": {
        "source": ROOT / "docs" / "PROJECT-zh.md",
        "output": ROOT / "docs" / "exports" / "kubectl-triage-project-v0.5.0-zh.docx",
        "title": "kubectl-triage",
        "subtitle": "项目文档",
        "tagline": "轻量、离线优先的 Kubernetes 事故诊断 CLI",
        "meta": "版本 v0.5.0   ·   2026-10-30   ·   Apache-2.0",
        "cover_note": "本文档由仓库 docs/PROJECT-zh.md 渲染生成，详细口径以各 ADR 与文档为准。",
        "header": "kubectl-triage 项目文档 · v0.5.0",
        "footer_prefix": "kubectl-triage · Apache-2.0 · 第 ",
        "footer_suffix": " 页",
        "body_font": "Helvetica Neue",
        "east_asia_font": "PingFang SC",
        "doc_title": "kubectl-triage 项目文档",
        "doc_subject": "kubectl-triage v0.5.0 项目综合文档",
        "doc_comments": "Rendered from docs/PROJECT-zh.md",
    },
}

BODY_FONT = "Helvetica Neue"
EAST_ASIA_FONT = "PingFang SC"


def apply_fonts(cfg):
    global BODY_FONT, EAST_ASIA_FONT
    BODY_FONT = cfg["body_font"]
    EAST_ASIA_FONT = cfg["east_asia_font"]


def set_font(run, size=10.5, bold=None, mono=False, color=None):
    run.font.name = MONO if mono else BODY_FONT
    rpr = run._element.get_or_add_rPr()
    rf = rpr.find(qn("w:rFonts"))
    if rf is None:
        rf = OxmlElement("w:rFonts")
        rpr.append(rf)
    rf.set(qn("w:eastAsia"), EAST_ASIA_FONT)
    if size is not None:
        run.font.size = Pt(size)
    if bold is not None:
        run.font.bold = bold
    if color is not None:
        run.font.color.rgb = color


def flush(par, text, size, color):
    if not text:
        return
    run = par.add_run(text)
    set_font(run, size, None, False, color)


def add_inline(par, text, size=10.5, color=None):
    i = 0
    buf = ""
    while i < len(text):
        if text.startswith("**", i):
            j = text.find("**", i + 2)
            if j != -1:
                flush(par, buf, size, color)
                buf = ""
                run = par.add_run(text[i + 2:j])
                set_font(run, size, True, False, color)
                i = j + 2
                continue
        if text.startswith(BT, i):
            j = text.find(BT, i + 1)
            if j != -1:
                flush(par, buf, size, color)
                buf = ""
                run = par.add_run(text[i + 1:j])
                set_font(run, size - 1, None, True, CODE)
                i = j + 1
                continue
        if text.startswith("[", i):
            j = text.find("](", i + 1)
            k = text.find(")", j + 2) if j != -1 else -1
            if j != -1 and k != -1:
                flush(par, buf, size, color)
                buf = ""
                run = par.add_run(text[i + 1:j])
                set_font(run, size, None, False, color)
                run2 = par.add_run(" (" + text[j + 2:k] + ")")
                set_font(run2, size - 1.5, None, False, MUTED)
                i = k + 1
                continue
        buf += text[i]
        i += 1
    flush(par, buf, size, color)


def shade(cell, fill):
    tc_pr = cell._tc.get_or_add_tcPr()
    shd = OxmlElement("w:shd")
    shd.set(qn("w:val"), "clear")
    shd.set(qn("w:color"), "auto")
    shd.set(qn("w:fill"), fill)
    tc_pr.append(shd)


def page_number(par):
    run = par.add_run()
    begin = OxmlElement("w:fldChar")
    begin.set(qn("w:fldCharType"), "begin")
    instr = OxmlElement("w:instrText")
    instr.set(qn("xml:space"), "preserve")
    instr.text = "PAGE"
    end = OxmlElement("w:fldChar")
    end.set(qn("w:fldCharType"), "end")
    run._r.append(begin)
    run._r.append(instr)
    run._r.append(end)
    set_font(run, 8.5, None, False, MUTED)


def build_styles(doc):
    normal = doc.styles["Normal"]
    normal.font.name = BODY_FONT
    normal.font.size = Pt(10.5)
    rpr = normal.element.get_or_add_rPr()
    rf = rpr.find(qn("w:rFonts"))
    if rf is None:
        rf = OxmlElement("w:rFonts")
        rpr.append(rf)
    rf.set(qn("w:eastAsia"), EAST_ASIA_FONT)
    normal.paragraph_format.space_after = Pt(4)
    normal.paragraph_format.line_spacing = 1.25

    specs = [
        ("Heading 1", 16, HEAD, 16, 8),
        ("Heading 2", 13, HEAD, 12, 6),
        ("Heading 3", 11.5, BLACK, 10, 4),
    ]
    for name, size, color, before, after in specs:
        st = doc.styles[name]
        st.font.name = BODY_FONT
        st.font.size = Pt(size)
        st.font.bold = True
        st.font.color.rgb = color
        rpr = st.element.get_or_add_rPr()
        rf = rpr.find(qn("w:rFonts"))
        if rf is None:
            rf = OxmlElement("w:rFonts")
            rpr.append(rf)
        rf.set(qn("w:eastAsia"), EAST_ASIA_FONT)
        st.paragraph_format.space_before = Pt(before)
        st.paragraph_format.space_after = Pt(after)
        st.paragraph_format.keep_with_next = True


def add_cover(doc, cfg):
    doc.add_paragraph().paragraph_format.space_before = Pt(150)

    title = doc.add_paragraph()
    title.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = title.add_run(cfg["title"])
    set_font(run, 36, True, False, HEAD)

    sub = doc.add_paragraph()
    sub.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = sub.add_run(cfg["subtitle"])
    set_font(run, 22, True, False, BLACK)

    tag = doc.add_paragraph()
    tag.alignment = WD_ALIGN_PARAGRAPH.CENTER
    tag.paragraph_format.space_before = Pt(18)
    run = tag.add_run(cfg["tagline"])
    set_font(run, 12, None, False, MUTED)

    meta = doc.add_paragraph()
    meta.alignment = WD_ALIGN_PARAGRAPH.CENTER
    meta.paragraph_format.space_before = Pt(40)
    run = meta.add_run(cfg["meta"])
    set_font(run, 11, None, False, RGBColor(51, 51, 51))

    repo = doc.add_paragraph()
    repo.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = repo.add_run("https://github.com/paas-ops/kubectl-triage")
    set_font(run, 10, None, False, MUTED)

    note = doc.add_paragraph()
    note.alignment = WD_ALIGN_PARAGRAPH.CENTER
    note.paragraph_format.space_before = Pt(120)
    run = note.add_run(cfg["cover_note"])
    set_font(run, 9, None, False, MUTED)

    brk = doc.add_paragraph()
    brk.add_run().add_break(WD_BREAK.PAGE)


def add_header_footer(doc, cfg):
    section = doc.sections[0]
    header = section.header.paragraphs[0]
    header.alignment = WD_ALIGN_PARAGRAPH.RIGHT
    run = header.add_run(cfg["header"])
    set_font(run, 8.5, None, False, MUTED)

    footer = section.footer.paragraphs[0]
    footer.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = footer.add_run(cfg["footer_prefix"])
    set_font(run, 8.5, None, False, MUTED)
    page_number(footer)
    run = footer.add_run(cfg["footer_suffix"])
    set_font(run, 8.5, None, False, MUTED)


def is_separator(row):
    if not row:
        return False
    for cell in row:
        text = cell.strip()
        if text == "":
            return False
        for ch in text:
            if ch not in "-: ":
                return False
    return True


def split_table_row(line):
    """Split a pipe-table row while ignoring pipes inside inline code spans."""
    text = line.strip()
    if text.startswith("|"):
        text = text[1:]
    if text.endswith("|"):
        text = text[:-1]
    cells = []
    buf = []
    in_code = False
    for ch in text:
        if ch == BT:
            in_code = not in_code
            buf.append(ch)
        elif ch == "|" and not in_code:
            cells.append("".join(buf).strip())
            buf = []
        else:
            buf.append(ch)
    cells.append("".join(buf).strip())
    return cells


def is_table_start(lines, i):
    if i + 1 >= len(lines):
        return False
    if "|" not in lines[i]:
        return False
    cells = split_table_row(lines[i + 1])
    return bool(cells) and all(is_separator([cell.replace(" ", "")]) for cell in cells)


def starts_block(line):
    stripped = line.strip()
    if not stripped:
        return True
    if stripped.startswith(FENCE) or stripped.startswith("#") or stripped.startswith(">"):
        return True
    if stripped.startswith("- ") or stripped.startswith("* "):
        return True
    if stripped in ("---", "***", "___"):
        return True
    dot = stripped.find(". ")
    return dot > 0 and stripped[:dot].isdigit()


def is_cjk(ch):
    code = ord(ch)
    return 0x2E80 <= code <= 0x9FFF or 0xF900 <= code <= 0xFAFF or 0xFF00 <= code <= 0xFFEF


def join_soft(lines):
    """Join Markdown soft-wrapped lines; CJK boundaries are joined without a space."""
    if not lines:
        return ""
    out = lines[0]
    for nxt in lines[1:]:
        if not out or not nxt:
            out += nxt
            continue
        if is_cjk(out[-1]) or is_cjk(nxt[0]):
            out += nxt
        else:
            out += " " + nxt
    return out


def add_table(doc, rows):
    if not rows:
        return
    header = rows[0]
    body = rows[1:]
    cols = len(header)
    table = doc.add_table(rows=1 + len(body), cols=cols)
    table.style = "Table Grid"
    table.alignment = 1
    for c, text in enumerate(header):
        cell = table.rows[0].cells[c]
        para = cell.paragraphs[0]
        add_inline(para, text, 9)
        for run in para.runs:
            run.font.bold = True
        shade(cell, "D9E2F3")
    for r, row in enumerate(body, start=1):
        for c in range(cols):
            text = row[c] if c < len(row) else ""
            add_inline(table.rows[r].cells[c].paragraphs[0], text, 9)
    doc.add_paragraph()


def add_code_block(doc, lines):
    para = doc.add_paragraph()
    para.paragraph_format.left_indent = Cm(0.35)
    para.paragraph_format.space_before = Pt(4)
    para.paragraph_format.space_after = Pt(6)
    para.paragraph_format.line_spacing = 1.1
    p_pr = para._p.get_or_add_pPr()
    shd = OxmlElement("w:shd")
    shd.set(qn("w:val"), "clear")
    shd.set(qn("w:color"), "auto")
    shd.set(qn("w:fill"), "F4F6F8")
    p_pr.append(shd)
    for idx, line in enumerate(lines):
        run = para.add_run(line)
        set_font(run, 8.5, None, True, CODE)
        if idx != len(lines) - 1:
            run.add_break()


def add_quote(doc, groups):
    para = doc.add_paragraph()
    para.paragraph_format.left_indent = Cm(0.4)
    para.paragraph_format.space_before = Pt(6)
    para.paragraph_format.space_after = Pt(6)
    p_pr = para._p.get_or_add_pPr()
    border = OxmlElement("w:pBdr")
    left = OxmlElement("w:left")
    left.set(qn("w:val"), "single")
    left.set(qn("w:sz"), "12")
    left.set(qn("w:space"), "6")
    left.set(qn("w:color"), "9DB7D5")
    border.append(left)
    p_pr.append(border)
    for idx, group in enumerate(groups):
        add_inline(para, join_soft(group), 9.5, MUTED)
        if idx != len(groups) - 1:
            para.add_run().add_break()


def render(doc, source):
    lines = source.read_text(encoding="utf-8").splitlines()
    i = 0
    skipped_title = False
    while i < len(lines):
        stripped = lines[i].strip()
        if not stripped:
            i += 1
            continue

        if stripped.startswith(FENCE):
            code = []
            i += 1
            while i < len(lines) and not lines[i].strip().startswith(FENCE):
                code.append(lines[i])
                i += 1
            i += 1
            add_code_block(doc, code)
            continue

        if stripped.startswith("|"):
            rows = []
            while i < len(lines) and lines[i].strip().startswith("|"):
                cells = split_table_row(lines[i])
                if not is_separator(cells):
                    rows.append(cells)
                i += 1
            add_table(doc, rows)
            continue

        if stripped == "---":
            i += 1
            continue

        if stripped.startswith(">"):
            groups = []
            current = []
            while i < len(lines) and lines[i].strip().startswith(">"):
                content = lines[i].strip().lstrip(">").strip()
                if content:
                    current.append(content)
                elif current:
                    groups.append(current)
                    current = []
                i += 1
            if current:
                groups.append(current)
            add_quote(doc, groups)
            continue

        if stripped.startswith("#"):
            level = 0
            while level < len(stripped) and stripped[level] == "#":
                level += 1
            text = stripped[level:].strip()
            i += 1
            if level == 1 and not skipped_title:
                skipped_title = True
                continue
            if 2 <= level <= 4:
                doc.add_heading(text, level=level - 1)
            continue

        if stripped.startswith("- ") or stripped.startswith("* "):
            style = "List Bullet"
            item = [stripped[2:]]
            i += 1
            while i < len(lines) and lines[i].strip() and not starts_block(lines[i]) and not is_table_start(lines, i):
                item.append(lines[i].strip())
                i += 1
            para = doc.add_paragraph(style=style)
            add_inline(para, join_soft(item), 10.5)
            continue

        dot = stripped.find(". ")
        if dot > 0 and stripped[:dot].isdigit():
            item = [stripped[dot + 2:]]
            i += 1
            while i < len(lines) and lines[i].strip() and not starts_block(lines[i]) and not is_table_start(lines, i):
                item.append(lines[i].strip())
                i += 1
            para = doc.add_paragraph(style="List Number")
            add_inline(para, join_soft(item), 10.5)
            continue

        paragraph = [stripped]
        i += 1
        while i < len(lines) and not starts_block(lines[i]) and not is_table_start(lines, i):
            paragraph.append(lines[i].strip())
            i += 1
        para = doc.add_paragraph()
        add_inline(para, join_soft(paragraph), 10.5)


def build_document(cfg):
    apply_fonts(cfg)
    doc = Document()
    section = doc.sections[0]
    section.page_width = Cm(21.0)
    section.page_height = Cm(29.7)
    section.top_margin = Cm(2.2)
    section.bottom_margin = Cm(2.2)
    section.left_margin = Cm(2.2)
    section.right_margin = Cm(2.2)

    build_styles(doc)
    add_header_footer(doc, cfg)
    add_cover(doc, cfg)
    render(doc, cfg["source"])

    props = doc.core_properties
    props.title = cfg["doc_title"]
    props.subject = cfg["doc_subject"]
    props.author = "paas-ops"
    props.comments = cfg["doc_comments"]

    output = cfg["output"]
    output.parent.mkdir(parents=True, exist_ok=True)
    doc.save(output)
    return output


def doc_stats(path):
    doc = Document(path)
    headings = [p for p in doc.paragraphs if p.style.name.startswith("Heading")]
    return {
        "paragraphs": len(doc.paragraphs),
        "tables": len(doc.tables),
        "headings": len(headings),
        "size_bytes": path.stat().st_size,
    }


def validate(path, cfg):
    stats = doc_stats(path)
    ok = stats["paragraphs"] >= 20 and stats["tables"] >= 5 and stats["headings"] >= 10
    doc = Document(path)
    cover_text = " ".join(p.text for p in doc.paragraphs[:10])
    if cfg["subtitle"] not in cover_text:
        ok = False
    header_text = " ".join(p.text for p in doc.sections[0].header.paragraphs)
    if cfg["header"] not in header_text:
        ok = False
    print("validated:", path)
    print("  paragraphs:", stats["paragraphs"])
    print("  tables:", stats["tables"])
    print("  headings:", stats["headings"])
    print("  size_bytes:", stats["size_bytes"])
    print("  structure_ok:", ok)
    return ok, stats


def parse_args(argv):
    parser = argparse.ArgumentParser(description="Render PROJECT Markdown into DOCX (en/zh).")
    parser.add_argument("--source", type=Path, default=None, help="Markdown source (default: language default)")
    parser.add_argument("--output", type=Path, default=None, help="DOCX output (default: language default)")
    parser.add_argument("--lang", choices=("en", "zh"), default=None, help="language of the document")
    parser.add_argument("--all", action="store_true", help="generate both default documents (default)")
    return parser.parse_args(argv)


def resolve_configs(args):
    if args.source or args.output:
        source = args.source or (LANGS["en"]["source"] if not args.lang or args.lang == "en" else LANGS["zh"]["source"])
        lang = args.lang
        if lang is None:
            lang = "zh" if "-zh" in str(source) or "-zh" in str(args.output or "") else "en"
        base = dict(LANGS[lang])
        if source is not None:
            base["source"] = source
        if args.output is not None:
            base["output"] = args.output
        return [base]
    if args.lang:
        return [dict(LANGS[args.lang])]
    return [dict(LANGS["en"]), dict(LANGS["zh"])]


def main(argv=None):
    args = parse_args(sys.argv[1:] if argv is None else argv)
    configs = resolve_configs(args)
    results = []
    for cfg in configs:
        if not cfg["source"].exists():
            print("missing source:", cfg["source"], file=sys.stderr)
            return 1
        output = build_document(cfg)
        ok, stats = validate(output, cfg)
        results.append((cfg, output, ok, stats))
        if not ok:
            print("structure validation failed for", output, file=sys.stderr)
            return 1

    if len(results) == 2:
        en = results[0][3]
        zh = results[1][3]
        match = en["headings"] == zh["headings"] and en["tables"] == zh["tables"]
        print("bilingual_structure_match:", match)
        print("  en headings/tables:", en["headings"], "/", en["tables"])
        print("  zh headings/tables:", zh["headings"], "/", zh["tables"])
        if not match:
            print("English and Chinese DOCX structures differ", file=sys.stderr)
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
