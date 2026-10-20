#!/usr/bin/env python3
"""Make the /monitoring landing page's two background images.

    python3 scripts/prepare_monitoring_art.py

Reads web/src/assets/brand/source/monitoring-hawk-eye.png (1672x941) and monitoring-earth-network.png (1774x887) and writes
web/src/assets/brand/monitoring-hawk-eye.webp and monitoring-earth-network.webp at full width as lossy WebP: they are photographic gradients, so
WebP is a fraction of the PNG's size with no visible banding at this quality. Needs Pillow built with WebP.
"""
from pathlib import Path

from PIL import Image

BRAND = Path(__file__).resolve().parent.parent / "web/src/assets/brand"
SOURCES = {"monitoring-hawk-eye": (1672, 941), "monitoring-earth-network": (1774, 887)}

for name, size in SOURCES.items():
    img = Image.open(BRAND / f"source/{name}.png").convert("RGB")
    if img.size != size:
        raise SystemExit(f"{name}: unexpected size {img.size}; this script was written for {size}")
    out = BRAND / f"{name}.webp"
    img.save(out, quality=82, method=6)
    print(out.name, img.size, out.stat().st_size, "bytes")
