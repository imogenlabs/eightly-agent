#!/usr/bin/env python3
"""Publish docs/wiki to an existing GitHub wiki checkout, using only the stdlib."""

import argparse
import json
import os
import posixpath
import re
import tempfile
import unittest
from pathlib import Path, PurePosixPath
from urllib.parse import quote, unquote, urlsplit

REPO_URL = "https://github.com/imogenlabs/eightly-agent"
BLOB_URL = REPO_URL + "/blob/main/"
RAW_URL = "https://raw.githubusercontent.com/imogenlabs/eightly-agent/main/"
DOCS_URL = "https://imogenlabs.github.io/eightly-agent/"
PAGE_SUFFIXES = {
    ".md", ".markdown", ".mdown", ".mediawiki", ".textile", ".rdoc", ".org",
    ".creole", ".rst", ".asciidoc", ".adoc", ".pod", ".rest", ".wiki",
}


def self_check():
    """Catch broken destinations, leaked MkDocs syntax, and unsafe stale-page cleanup."""
    check = unittest.TestCase()
    with tempfile.TemporaryDirectory(prefix="wiki-check-") as folder:
        root = Path(folder)
        repo, wiki = root / "source", root / "wiki"
        docs = repo / "docs/wiki"
        docs.mkdir(parents=True)
        (wiki / ".git").mkdir(parents=True)
        (repo / "docs/images").mkdir()
        (repo / "docs/images/screen.png").write_bytes(b"fixture")
        for name in ("LICENSE", "SECURITY.md", "CHANGELOG.md"):
            (repo / name).write_text("fixture\n", encoding="utf-8")
        (repo / "mkdocs.yml").write_text(
            "site_name: Fixture\nnav:\n  - Home: index.md\n  - Getting Started:\n"
            "      - Installation: Installation.md\n", encoding="utf-8"
        )
        (docs / "index.md").write_text(
            '---\ndescription: "Fixture"\n---\n\n# Home\n\n'
            '[Install](Installation.md#docker) [Home](index.md)\n'
            '[Security](../../SECURITY.md) [License](LICENSE)\n'
            '[Changes](CHANGELOG.md)\n'
            '![Screen](../images/screen.png){ width="100%" }\n'
            '<img src="../images/screen.png" width="100%" />\n'
            '[guide]: Installation.md#docker "Guide"\n\n'
            '!!! note "Read this"\n    Keep this.\n\n'
            '    !!! warning\n        Nested note.\n\n'
            '=== "Docker"\n    Use Docker.\n\n'
            '## Heading { #custom }\n\n'
            '```mermaid\nflowchart LR\n  A["Installation.md#docker"] --> B\n```\n\n'
            '```markdown\n!!! note\n[Example](Installation.md)\n```\n', encoding="utf-8"
        )
        (docs / "Installation.md").write_text(
            '# Installation\n\n## Docker\n\n[Home](index.md)\n', encoding="utf-8"
        )
        (wiki / "Old.md").write_text("old", encoding="utf-8")
        (wiki / "Old.textile").write_text("old", encoding="utf-8")
        (wiki / "logo.png").write_bytes(b"keep")
        (wiki / ".git/keep.md").write_text("keep", encoding="utf-8")
        removed = publish(repo, wiki)
        home = (wiki / "Home.md").read_text(encoding="utf-8")
        for expected in (
            '[Install](Installation#docker) [Home](Home)',
            '[Security](' + BLOB_URL + 'SECURITY.md)',
            '[License](' + BLOB_URL + 'LICENSE)',
            '[Changes](' + BLOB_URL + 'CHANGELOG.md)',
            '![Screen](' + RAW_URL + 'docs/images/screen.png)',
            '<img src="' + RAW_URL + 'docs/images/screen.png" width="100%" />',
            '[guide]: Installation#docker "Guide"',
            '**Read this**\n\n> Keep this.',
            '> **Warning**\n>\n> > Nested note.',
            '**Docker**\n\nUse Docker.',
            '## Heading\n',
            '```mermaid\nflowchart LR\n  A["Installation.md#docker"] --> B\n```',
            '```markdown\n!!! note\n[Example](Installation.md)\n```',
        ):
            check.assertIn(expected, home)
        check.assertNotIn('description:', home)
        check.assertNotIn('{ width=', home)
        check.assertEqual(removed, ["Old.md", "Old.textile"])
        check.assertTrue((wiki / "logo.png").exists())
        check.assertTrue((wiki / ".git/keep.md").exists())
        sidebar = (wiki / "_Sidebar.md").read_text(encoding="utf-8")
        check.assertIn('## Getting Started\n\n- [Installation](Installation)', sidebar)
        check.assertIn('[Home](Home)', sidebar)
        for name, source in (("Home.md", "docs/wiki/index.md"),
                             ("Installation.md", "docs/wiki/Installation.md"),
                             ("_Sidebar.md", "mkdocs.yml"),
                             ("_Footer.md", "scripts/publish-wiki.py")):
            first_line = (wiki / name).read_text(encoding="utf-8").splitlines()[0]
            check.assertEqual(first_line, source_notice(source).rstrip())
        snapshot = {p.name: p.read_bytes() for p in wiki.glob("*.md")}
        check.assertEqual(publish(repo, wiki), [])
        check.assertEqual(snapshot, {p.name: p.read_bytes() for p in wiki.glob("*.md")})
        with check.assertRaisesRegex(ValueError, "Missing local link"):
            rewrite_url("Missing.md", PurePosixPath("index.md"), repo, {"index.md": "Home.md"})
        (repo / "mkdocs.yml").write_text("nav:\n  - Missing: Missing.md\n", encoding="utf-8")
        with check.assertRaisesRegex(ValueError, "Missing nav page"):
            publish(repo, wiki)
        check.assertEqual(snapshot, {p.name: p.read_bytes() for p in wiki.glob("*.md")})
    print("Wiki publisher self-check passed.")


def source_notice(source):
    return ("This page is published from the repository. Edit it there: "
            f"[{source}]({BLOB_URL}{quote(source, safe='/')}).\n")


def nav_entries(config):
    """Read this repo's block-list nav, without loading MkDocs' Python YAML tags.

    Fail on other YAML shapes so a nav change cannot silently drop wiki links.
    """
    lines = config.splitlines()
    start = lines.index("nav:") + 1
    groups = []
    entries = []
    for line in lines[start:]:
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if not line.startswith(" "):
            break
        match = re.fullmatch(r"( +)- (.+?):(?: +(.*))?", line)
        if not match:
            raise ValueError(f"Unsupported nav entry: {line}")
        spaces, label, target = match.groups()
        if label.startswith('"'):
            label = json.loads(label)
        elif label.startswith("'") and label.endswith("'"):
            label = label[1:-1].replace("''", "'")
        while groups and groups[-1] >= len(spaces):
            groups.pop()
        if target:
            if target.startswith('"'):
                target = json.loads(target)
            elif target.startswith("'") and target.endswith("'"):
                target = target[1:-1].replace("''", "'")
        entries.append((len(groups), label, target))
        if not target:
            groups.append(len(spaces))
    if not entries:
        raise ValueError("Empty nav")
    return entries


def rewrite_url(url, current, repo, pages, image=False):
    parsed = urlsplit(url)
    if parsed.scheme or parsed.netloc or not parsed.path:
        return url
    path = unquote(parsed.path)
    if path in {"LICENSE", "SECURITY.md", "CHANGELOG.md"}:
        target = path
    elif path.startswith("/"):
        target = posixpath.normpath(path.lstrip("/"))
    else:
        target = posixpath.normpath(posixpath.join("docs/wiki", str(current.parent), path))
    if target == ".." or target.startswith("../") or not (repo / target).is_file():
        raise ValueError(f"Missing local link in {current}: {url}")
    page = target.removeprefix("docs/wiki/")
    if target.startswith("docs/wiki/") and page in pages and not image:
        result = quote(str(PurePosixPath(pages[page]).with_suffix("")), safe="/")
    elif image or PurePosixPath(target).suffix.lower() in {
        ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".avif",
    }:
        result = RAW_URL + quote(target, safe="/")
    else:
        result = BLOB_URL + quote(target, safe="/")
    if parsed.query:
        result += "?" + parsed.query
    if parsed.fragment:
        result += "#" + parsed.fragment
    return result


def outside_fences(text, transform):
    """Apply prose rewriting without changing code, including Mermaid blocks."""
    out, prose = [], []
    fence = None
    for line in text.splitlines(keepends=True):
        match = re.match(r"^\s*(`{3,}|~{3,})", line)
        if fence:
            out.append(line)
            if match and match[1][0] == fence[0] and len(match[1]) >= len(fence):
                fence = None
        elif match:
            out.append(transform("".join(prose)))
            prose = []
            out.append(line)
            fence = match[1]
        else:
            prose.append(line)
    out.append(transform("".join(prose)))
    return "".join(out)


def plain_blocks(text):
    """Flatten MkDocs admonitions and tabs; recurse for nested blocks."""
    lines = text.splitlines()
    out = []
    i, fence = 0, None
    while i < len(lines):
        line = lines[i]
        marker = re.match(r"^\s*(`{3,}|~{3,})", line)
        if fence or marker:
            out.append(line)
            if fence and marker and marker[1][0] == fence[0] and len(marker[1]) >= len(fence):
                fence = None
            elif not fence:
                fence = marker[1]
            i += 1
            continue
        note = re.fullmatch(r'( *)(?:!!!|\?\?\?\+?) +([\w-]+)(?: +"([^"]*)")?', line)
        tab = re.fullmatch(r'( *)=== +"([^"]+)"', line)
        if not note and not tab:
            out.append(line)
            i += 1
            continue
        indent = len((note or tab)[1])
        title = (note[3] or note[2].replace("-", " ").title()) if note else tab[2]
        body = []
        i += 1
        while i < len(lines):
            child = lines[i]
            if child.strip() and not child.startswith(" " * (indent + 4)):
                break
            body.append(child[indent + 4:] if child.strip() else "")
            i += 1
        body = plain_blocks("\n".join(body)).strip("\n")
        prefix = " " * indent
        out += [prefix + f"**{title}**", ""]
        if note:
            out.extend(prefix + ("> " + child if child else ">") for child in body.splitlines())
        else:
            out.extend(prefix + child if child else "" for child in body.splitlines())
        out.append("")
    return "\n".join(out) + "\n"


def render_markdown(text, current, repo, pages):
    text = re.sub(r"\A---\r?\n.*?\r?\n---\r?\n+", "", text, count=1, flags=re.S)

    def prose(chunk):
        def destination(match):
            url = match[2]
            angled = url.startswith("<")
            url = url[1:-1] if angled else url
            rewritten = rewrite_url(url, current, repo, pages, match[1].startswith("!"))
            return match[1] + (f"<{rewritten}>" if angled else rewritten)

        # Inline links/images and reference-style destinations (titles are kept).
        chunk = re.sub(r'(!?\[[^\]\n]*\]\()(<[^>\n]+>|[^\s)\n]+)', destination, chunk)
        chunk = re.sub(r'(?m)^( *\[[^\]\n]+\]: *)(<[^>\n]+>|[^\s\n]+)', destination, chunk)

        def html_link(match):
            return (match[1] + match[2]
                    + rewrite_url(match[3], current, repo, pages,
                                  match[1].lower().lstrip().startswith("src")) + match[2])

        chunk = re.sub(r'(\b(?:src|href)\s*=\s*)([\'"])(.*?)\2', html_link, chunk, flags=re.I)
        chunk = re.sub(r'\s+markdown=([\'"])(?:1|block|span)\1', "", chunk)
        return re.sub(r'[ \t]*\{[ \t]*(?:[.:#]|[\w-]+=)[^{}\n]*\}', "", chunk)

    return plain_blocks(outside_fences(text, prose))


def build_pages(repo):
    docs = repo / "docs/wiki"
    sources = sorted(docs.rglob("*.md"))
    if not (docs / "index.md").is_file():
        raise ValueError("docs/wiki/index.md is required")
    pages = {p.relative_to(docs).as_posix():
             "Home.md" if p.name == "index.md" and p.parent == docs
             else p.relative_to(docs).as_posix() for p in sources}
    if len(set(pages.values())) != len(pages) or any(
        name in pages.values() for name in ("_Sidebar.md", "_Footer.md")
    ):
        raise ValueError("Source pages collide with Home, _Sidebar or _Footer")
    output = {}
    for page in sources:
        relative = page.relative_to(docs).as_posix()
        output[pages[relative]] = source_notice("docs/wiki/" + relative) + "\n" + render_markdown(
            page.read_text(encoding="utf-8"), PurePosixPath(relative), repo, pages)
    sidebar = [source_notice("mkdocs.yml"), ""]
    for depth, label, target in nav_entries((repo / "mkdocs.yml").read_text(encoding="utf-8")):
        if target:
            if target not in pages:
                raise ValueError(f"Missing nav page: {target}")
            dest = quote(str(PurePosixPath(pages[target]).with_suffix("")), safe="/")
            sidebar.append(f"- [{label}]({dest})")
        else:
            sidebar += ["", "#" * (depth + 2) + " " + label, ""]
    output["_Sidebar.md"] = "\n".join(sidebar).rstrip() + "\n"
    output["_Footer.md"] = (source_notice("scripts/publish-wiki.py") + "\n"
                            f"[Docs]({DOCS_URL}) · [Repository]({REPO_URL}) · "
                            f"[Apache License 2.0]({BLOB_URL}LICENSE)\n")
    return output


def publish(repo, wiki):
    repo, wiki = repo.resolve(), wiki.resolve()
    if not (wiki / ".git").is_dir() or repo == wiki or repo in wiki.parents or wiki in repo.parents:
        raise ValueError("Use separate source and wiki Git checkouts")
    output = build_pages(repo)  # Validate all input before changing the wiki.
    existing = [p for p in wiki.rglob("*")
                if ".git" not in p.relative_to(wiki).parts
                and p.is_file() and p.suffix.lower() in PAGE_SUFFIXES]
    if any(p.is_symlink() for p in existing) or any(
        (wiki / name).is_symlink() for name in output
    ):
        raise ValueError("Wiki pages must be regular files, not symlinks")
    removed = sorted(p.relative_to(wiki).as_posix() for p in existing
                     if p.relative_to(wiki).as_posix() not in output)
    for name, body in output.items():
        destination = wiki / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(body, encoding="utf-8")
    for name in removed:
        (wiki / name).unlink()
    return removed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="Run isolated self-check fixtures")
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--wiki", type=Path, help="Existing wiki Git checkout")
    args = parser.parse_args()
    if args.check:
        self_check()
        build_pages(args.source.resolve())
        print("Source pages and navigation checked.")
        return
    if args.wiki is None:
        parser.error("--wiki is required unless --check is used")
    removed = publish(args.source, args.wiki)
    summary = "## Wiki publication\n\nRebuilt pages from `docs/wiki/`.\n\n"
    summary += ("Removed stale pages:\n\n" + "\n".join(f"- `{p}`" for p in removed)
                if removed else "No stale pages to remove.")
    print(summary)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as report:
            report.write(summary + "\n")


if __name__ == "__main__":
    main()
