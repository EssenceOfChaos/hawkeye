#!/usr/bin/env python3
"""Crop and compress the captured screens into web/src/assets/brand/screens/*.webp.

    python3 scripts/screenshots/process.py <dir with dashboard.png, incidents.png, alert-outage.png>

The captures are retina (2x) PNGs; each is cropped to the part worth showing (no footer, no empty space) and resized to about twice its display size
as lossy WebP. Needs Pillow built with WebP.
"""
import sys
from pathlib import Path

from PIL import Image

src = Path(sys.argv[1])
out = Path(__file__).resolve().parent.parent.parent / "web/src/assets/brand/screens"
out.mkdir(parents=True, exist_ok=True)

# name, source file, crop box in captured pixels (left, top, right, bottom), output width
JOBS = [
    ("dashboard", "dashboard.png", (0, 0, 2560, 2340), 1700),
    ("incidents", "incidents.png", (0, 0, 2560, 880), 1400),
    ("alert-email", "alert-outage.png", (0, 0, 1280, 1300), 960),
]
for name, file, box, width in JOBS:
    im = Image.open(src / file).convert("RGB").crop(box)
    im = im.resize((width, round(im.height * width / im.width)), Image.LANCZOS)
    path = out / f"{name}.webp"
    im.save(path, quality=84, method=6)
    print(f"{name}: {im.size[0]}x{im.size[1]}  {path.stat().st_size // 1024} KB")
