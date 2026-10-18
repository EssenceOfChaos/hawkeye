#!/usr/bin/env python3
"""Make the landing page's hero laptop image from the glossy mockup.

    python3 scripts/prepare_hero_laptop.py

Reads web/src/assets/brand/source/dashboard-laptop-mockup.png (1387x1134, transparent background) and writes web/src/assets/brand/dashboard-laptop.webp:
1100px wide (about 2x the size it is shown at) as lossy WebP with its alpha channel, a fraction of the PNG's size. Needs Pillow built with WebP.
"""
from pathlib import Path

from PIL import Image

BRAND = Path(__file__).resolve().parent.parent / "web/src/assets/brand"
WIDTH = 1100

img = Image.open(BRAND / "source/dashboard-laptop-mockup.png").convert("RGBA")
if img.size != (1387, 1134):
    raise SystemExit(f"unexpected size {img.size}; this script was written for 1387x1134")
out = img.resize((WIDTH, round(img.height * WIDTH / img.width)), Image.LANCZOS)
out.save(BRAND / "dashboard-laptop.webp", quality=88, method=6)
print("dashboard-laptop.webp", out.size, (BRAND / "dashboard-laptop.webp").stat().st_size, "bytes")
