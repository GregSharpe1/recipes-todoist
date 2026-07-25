#!/usr/bin/env python3
"""Report Schema.org Recipe data from a URL or local HTML fixture."""

from __future__ import annotations

import argparse
import html
import json
import sys
from html.parser import HTMLParser
from pathlib import Path
from typing import Any
from urllib.parse import urlparse
from urllib.request import Request, urlopen

DEFAULT_MAX_BYTES = 4 << 20
USER_AGENT = "todoist-recipes/1.0 (+recipe importer)"


class JSONLDScriptParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self._capturing = False
        self._parts: list[str] = []
        self.scripts: list[str] = []

    def handle_starttag(
        self, tag: str, attrs: list[tuple[str, str | None]]
    ) -> None:
        if tag.lower() != "script" or self._capturing:
            return
        values = {key.lower(): (value or "") for key, value in attrs}
        if values.get("type", "").lower() == "application/ld+json":
            self._capturing = True
            self._parts = []

    def handle_data(self, data: str) -> None:
        if self._capturing:
            self._parts.append(data)

    def handle_endtag(self, tag: str) -> None:
        if tag.lower() == "script" and self._capturing:
            self.scripts.append("".join(self._parts))
            self._capturing = False
            self._parts = []


def recipe_types(value: Any) -> list[str]:
    if isinstance(value, str):
        return [value]
    if isinstance(value, list):
        return [item for item in value if isinstance(item, str)]
    return []


def find_recipe_nodes(value: Any) -> list[dict[str, Any]]:
    found: list[dict[str, Any]] = []
    if isinstance(value, list):
        for item in value:
            found.extend(find_recipe_nodes(item))
    elif isinstance(value, dict):
        if any(item.lower() == "recipe" for item in recipe_types(value.get("@type"))):
            found.append(value)
        for item in value.values():
            found.extend(find_recipe_nodes(item))
    return found


def first_text(value: Any) -> str:
    if isinstance(value, str):
        return value.strip()
    if isinstance(value, list):
        for item in value:
            result = first_text(item)
            if result:
                return result
    return ""


def image_url(value: Any) -> str:
    if isinstance(value, str):
        return value.strip()
    if isinstance(value, list):
        for item in value:
            result = image_url(item)
            if result:
                return result
    if isinstance(value, dict):
        for key in ("url", "@id", "contentUrl"):
            result = first_text(value.get(key))
            if result:
                return result
    return ""


def ingredient_texts(value: Any) -> list[str]:
    if isinstance(value, str):
        return [value.strip()] if value.strip() else []
    if isinstance(value, list):
        return [item.strip() for item in value if isinstance(item, str) and item.strip()]
    return []


def read_source(source: str, timeout: float, max_bytes: int) -> tuple[str, str]:
    parsed = urlparse(source)
    if parsed.scheme:
        if parsed.scheme not in {"http", "https"}:
            raise ValueError("only HTTP(S) URLs are supported")
        request = Request(source, headers={"User-Agent": USER_AGENT})
        with urlopen(request, timeout=timeout) as response:
            body = response.read(max_bytes + 1)
            if len(body) > max_bytes:
                raise ValueError(f"response exceeds {max_bytes} bytes")
            charset = response.headers.get_content_charset() or "utf-8"
            return body.decode(charset, errors="replace"), response.geturl()

    path = Path(source)
    body = path.read_bytes()
    if len(body) > max_bytes:
        raise ValueError(f"file exceeds {max_bytes} bytes")
    return body.decode("utf-8", errors="replace"), str(path.resolve())


def inspect(source: str, timeout: float, max_bytes: int) -> dict[str, Any]:
    raw_html, resolved_source = read_source(source, timeout, max_bytes)
    parser = JSONLDScriptParser()
    parser.feed(raw_html)

    invalid_scripts = 0
    recipes: list[dict[str, Any]] = []
    for script in parser.scripts:
        try:
            payload = json.loads(html.unescape(script.strip()))
        except (json.JSONDecodeError, TypeError):
            invalid_scripts += 1
            continue
        recipes.extend(find_recipe_nodes(payload))

    summaries = []
    for recipe in recipes:
        ingredients = ingredient_texts(recipe.get("recipeIngredient"))
        summaries.append(
            {
                "name": first_text(recipe.get("name"))
                or first_text(recipe.get("headline")),
                "yield": first_text(recipe.get("recipeYield")),
                "image_url": image_url(recipe.get("image")),
                "ingredient_count": len(ingredients),
                "ingredients": ingredients,
            }
        )

    return {
        "source": source,
        "resolved_source": resolved_source,
        "json_ld_script_count": len(parser.scripts),
        "invalid_json_ld_script_count": invalid_scripts,
        "recipe_count": len(summaries),
        "recipes": summaries,
    }


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Inspect Schema.org Recipe JSON-LD in a live page or HTML fixture."
    )
    parser.add_argument("source", help="HTTP(S) recipe URL or local HTML file")
    parser.add_argument("--timeout", type=float, default=15.0)
    parser.add_argument("--max-bytes", type=int, default=DEFAULT_MAX_BYTES)
    args = parser.parse_args()

    if args.timeout <= 0 or args.max_bytes <= 0:
        parser.error("--timeout and --max-bytes must be positive")

    try:
        report = inspect(args.source, args.timeout, args.max_bytes)
    except Exception as exc:
        print(f"inspection failed: {exc}", file=sys.stderr)
        return 1

    print(json.dumps(report, indent=2, ensure_ascii=False))
    return 0 if report["recipe_count"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
