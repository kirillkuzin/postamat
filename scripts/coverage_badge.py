#!/usr/bin/env python3
"""Generate a deterministic SVG coverage badge from `go tool cover -func` output."""

from __future__ import annotations

import html
import re
import sys
from pathlib import Path


def color_for(percent: float) -> str:
    if percent >= 90:
        return "#4c1"  # brightgreen
    if percent >= 80:
        return "#97ca00"  # green
    if percent >= 70:
        return "#dfb317"  # yellowgreen
    if percent >= 60:
        return "#fe7d37"  # orange
    return "#e05d44"  # red


def text_width(text: str) -> int:
    # Shields-style approximation that keeps the badge compact and deterministic.
    return 10 + len(text) * 7


def parse_total(path: Path) -> float:
    total_re = re.compile(r"^total:\s+\(statements\)\s+([0-9]+(?:\.[0-9]+)?)%$")
    for line in path.read_text(encoding="utf-8").splitlines():
        match = total_re.match(line.strip())
        if match:
            return float(match.group(1))
    raise SystemExit(f"coverage total not found in {path}")


def render(percent: float) -> str:
    label = "coverage"
    value = f"{percent:.0f}%"
    left_width = text_width(label)
    right_width = text_width(value)
    total_width = left_width + right_width
    left_center = left_width / 2
    right_center = left_width + right_width / 2
    color = color_for(percent)
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{total_width}" height="20" role="img" aria-label="{html.escape(label)}: {html.escape(value)}">
  <title>{html.escape(label)}: {html.escape(value)}</title>
  <linearGradient id="s" x2="0" y2="100%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <clipPath id="r">
    <rect width="{total_width}" height="20" rx="3" fill="#fff"/>
  </clipPath>
  <g clip-path="url(#r)">
    <rect width="{left_width}" height="20" fill="#555"/>
    <rect x="{left_width}" width="{right_width}" height="20" fill="{color}"/>
    <rect width="{total_width}" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="110">
    <text aria-hidden="true" x="{left_center * 10:.0f}" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="{(left_width - 10) * 10}">{html.escape(label)}</text>
    <text x="{left_center * 10:.0f}" y="140" transform="scale(.1)" fill="#fff" textLength="{(left_width - 10) * 10}">{html.escape(label)}</text>
    <text aria-hidden="true" x="{right_center * 10:.0f}" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="{(right_width - 10) * 10}">{html.escape(value)}</text>
    <text x="{right_center * 10:.0f}" y="140" transform="scale(.1)" fill="#fff" textLength="{(right_width - 10) * 10}">{html.escape(value)}</text>
  </g>
</svg>
"""


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print("usage: coverage_badge.py COVERAGE_TXT OUTPUT_SVG", file=sys.stderr)
        return 2
    coverage_path = Path(argv[1])
    output_path = Path(argv[2])
    percent = parse_total(coverage_path)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(render(percent), encoding="utf-8")
    print(f"wrote {output_path} for {percent:.1f}%")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
