"""Text shaping for the UI, kept Qt-free so it can be tested without a display.

Qt does not wrap a plain-text tooltip: it lays it out on one line however wide that
turns out to be. The cheat notes are two or three sentences, so they rendered as a single
line running off the edge of the screen. Wrapping them here keeps the tooltip a readable
block and avoids depending on rich text (which wraps, but then needs the note escaped).
"""

from __future__ import annotations

import textwrap

WIDTH = 72          # comfortable for a tooltip: long enough not to look ragged


def wrap(text: str, width: int = WIDTH) -> str:
    """Re-flow a tooltip to ``width`` columns, preserving deliberate blank lines."""
    if not text:
        return ""
    blocks = text.split("\n\n")
    out = []
    for block in blocks:
        # collapse the source's own line breaks, then re-flow: notes are written as flowing
        # prose in the source and should not inherit its wrapping
        flat = " ".join(block.split())
        out.append(textwrap.fill(flat, width=width) if flat else "")
    return "\n\n".join(out)
