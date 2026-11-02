"""Write llms.txt (https://llmstxt.org) from the nav and each page's description.

Every page must carry a `description` in its front matter. The build fails
without one, so llms.txt cannot fall behind the pages.
"""

import os

from mkdocs.exceptions import PluginError

_nav = None


def on_nav(nav, **kwargs):
    global _nav
    _nav = nav
    return nav


def on_post_build(config, **kwargs):
    lines = [f"# {config['site_name']}", "", f"> {config['site_description']}", ""]
    for item in _nav.items:
        pages = item.children if item.is_section else [item]
        lines += [f"## {item.title if item.is_section else 'Overview'}", ""]
        for page in pages:
            desc = (page.meta or {}).get("description")
            if not desc:
                raise PluginError(f"{page.file.src_uri} has no description in its front matter")
            lines.append(f"- [{page.title}]({page.canonical_url}): {desc}")
        lines.append("")
    with open(os.path.join(config["site_dir"], "llms.txt"), "w", encoding="utf-8") as f:
        f.write("\n".join(lines))
