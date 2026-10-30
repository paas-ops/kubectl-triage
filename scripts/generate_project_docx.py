#!/usr/bin/env python3
"""Render docs/PROJECT.md into a Word project document (standalone helper)."""

from pathlib import Path

from docx import Document
from docx.enum.text import WD_ALIGN_PARAGRAPH, WD_BREAK
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.shared import Cm, Pt, RGBColor

SRC = Path("docs/PROJECT.md")
OUT = Path("docs/exports/kubectl-triage-project-v0.5.0.docx")
BT = chr(96)
FONT = "PingFang SC"
LATIN = "Helvetica Neue"
MONO = "Menlo"
HEAD = RGBColor(11, 61, 145)
MUTED = RGBColor(89, 89, 89)
CODE = RGBColor(36, 41, 46)
BLACK = RGBColor(31, 42, 68)


def set_font(run, size=10.5, bold=None, mono=False, color=None):
    run.font.name = MONO if mono else LATIN
    rpr = run._element.get_or_add_rPr()
    rf = rpr.find(qn("w:rFonts"))
    if rf is None:
        rf = OxmlElement("w:rFonts")
        rpr.append(rf)
    rf.set(qn("w:eastAsia"), FONT)
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
    normal.font.name = LATIN
    normal.font.size = Pt(10.5)
    rpr = normal.element.get_or_add_rPr()
    rf = rpr.find(qn("w:rFonts"))
    if rf is None:
        rf = OxmlElement("w:rFonts")
        rpr.append(rf)
    rf.set(qn("w:eastAsia"), FONT)
    normal.paragraph_format.space_after = Pt(4)
    normal.paragraph_format.line_spacing = 1.25

    specs = [
        ("Heading 1", 16, HEAD, 16, 8),
        ("Heading 2", 13, HEAD, 12, 6),
        ("Heading 3", 11.5, BLACK, 10, 4),
    ]
    for name, size, color, before, after in specs:
        st = doc.styles[name]
        st.font.name = LATIN
        st.font.size = Pt(size)
        st.font.bold = True
        st.font.color.rgb = color
        rpr = st.element.get_or_add_rPr()
        rf = rpr.find(qn("w:rFonts"))
        if rf is None:
            rf = OxmlElement("w:rFonts")
            rpr.append(rf)
        rf.set(qn("w:eastAsia"), FONT)
        st.paragraph_format.space_before = Pt(before)
        st.paragraph_format.space_after = Pt(after)
        st.paragraph_format.keep_with_next = True


def add_cover(doc):
    doc.add_paragraph().paragraph_format.space_before = Pt(150)

    title = doc.add_paragraph()
    title.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = title.add_run("kubectl-triage")
    set_font(run, 36, True, False, HEAD)

    sub = doc.add_paragraph()
    sub.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = sub.add_run("项目文档")
    set_font(run, 22, True, False, BLACK)

    tag = doc.add_paragraph()
    tag.alignment = WD_ALIGN_PARAGRAPH.CENTER
    tag.paragraph_format.space_before = Pt(18)
    run = tag.add_run("轻量、离线优先的 Kubernetes 事故诊断 CLI")
    set_font(run, 12, None, False, MUTED)

    meta = doc.add_paragraph()
    meta.alignment = WD_ALIGN_PARAGRAPH.CENTER
    meta.paragraph_format.space_before = Pt(40)
    run = meta.add_run("版本 v0.5.0   ·   2026-10-30   ·   Apache-2.0")
    set_font(run, 11, None, False, RGBColor(51, 51, 51))

    repo = doc.add_paragraph()
    repo.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = repo.add_run("https://github.com/paas-ops/kubectl-triage")
    set_font(run, 10, None, False, MUTED)

    note = doc.add_paragraph()
    note.alignment = WD_ALIGN_PARAGRAPH.CENTER
    note.paragraph_format.space_before = Pt(120)
    run = note.add_run("本文档由仓库 docs/PROJECT.md 渲染生成，详细口径以各 ADR 与文档为准。")
    set_font(run, 9, None, False, MUTED)

    brk = doc.add_paragraph()
    brk.add_run().add_break(WD_BREAK.PAGE)


def add_header_footer(doc):
    section = doc.sections[0]
    header = section.header.paragraphs[0]
    header.alignment = WD_ALIGN_PARAGRAPH.RIGHT
    run = header.add_run("kubectl-triage 项目文档 · v0.5.0")
    set_font(run, 8.5, None, False, MUTED)

    footer = section.footer.paragraphs[0]
    footer.alignment = WD_ALIGN_PARAGRAPH.CENTER
    run = footer.add_run("kubectl-triage · Apache-2.0 · 第 ")
    set_font(run, 8.5, None, False, MUTED)
    page_number(footer)
    run = footer.add_run(" 页")
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
        add_inline(para, text.strip(), 9)
        for run in para.runs:
            run.font.bold = True
        shade(cell, "D9E2F3")
    for r, row in enumerate(body, start=1):
        for c in range(cols):
            text = row[c].strip() if c < len(row) else ""
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


def add_quote(doc, lines):
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
    for idx, text in enumerate(lines):
        add_inline(para, text, 9.5, MUTED)
        if idx != len(lines) - 1:
            para.add_run().add_break()


def render(doc):
    lines = SRC.read_text(encoding="utf-8").splitlines()
    i = 0
    skipped_title = False
    while i < len(lines):
        stripped = lines[i].strip()
        if not stripped:
            i += 1
            continue

        fence = BT * 3
        if stripped.startswith(fence):
            code = []
            i += 1
            while i < len(lines) and not lines[i].strip().startswith(fence):
                code.append(lines[i])
                i += 1
            i += 1
            add_code_block(doc, code)
            continue

        if stripped.startswith("|"):
            rows = []
            while i < len(lines) and lines[i].strip().startswith("|"):
                cells = [c.strip() for c in lines[i].strip().strip("|").split("|")]
                if not is_separator(cells):
                    rows.append(cells)
                i += 1
            add_table(doc, rows)
            continue

        if stripped == "---":
            i += 1
            continue

        if stripped.startswith(">"):
            quote = []
            while i < len(lines) and lines[i].strip().startswith(">"):
                quote.append(lines[i].strip().lstrip(">").strip())
                i += 1
            add_quote(doc, quote)
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
            para = doc.add_paragraph(style="List Bullet")
            add_inline(para, stripped[2:], 10.5)
            i += 1
            continue

        dot = stripped.find(". ")
        if dot > 0 and stripped[:dot].isdigit():
            para = doc.add_paragraph(style="List Number")
            add_inline(para, stripped[dot + 2:], 10.5)
            i += 1
            continue

        para = doc.add_paragraph()
        add_inline(para, stripped, 10.5)
        i += 1


def main():
    doc = Document()
    section = doc.sections[0]
    section.page_width = Cm(21.0)
    section.page_height = Cm(29.7)
    section.top_margin = Cm(2.2)
    section.bottom_margin = Cm(2.2)
    section.left_margin = Cm(2.2)
    section.right_margin = Cm(2.2)

    build_styles(doc)
    add_header_footer(doc)
    add_cover(doc)
    render(doc)

    props = doc.core_properties
    props.title = "kubectl-triage 项目文档"
    props.subject = "kubectl-triage v0.5.0 项目综合文档"
    props.author = "paas-ops"
    props.comments = "Rendered from docs/PROJECT.md"

    OUT.parent.mkdir(parents=True, exist_ok=True)
    doc.save(OUT)

    check = Document(OUT)
    headings = [p for p in check.paragraphs if p.style.name.startswith("Heading")]
    print("saved:", OUT)
    print("size_bytes:", OUT.stat().st_size)
    print("paragraphs:", len(check.paragraphs))
    print("tables:", len(check.tables))
    print("headings:", len(headings))
    print("headings_sample:", [p.text for p in headings[:8]])


if __name__ == "__main__":
    main()
