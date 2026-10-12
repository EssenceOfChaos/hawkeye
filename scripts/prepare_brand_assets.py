#!/usr/bin/env python3
"""Prepare the HawkEye brand pack for the web app.

Usage: python3 scripts/prepare_brand_assets.py /path/to/hawkeye_transparent_assets

The pack's PNGs are crops of a generated concept sheet. Some crops contain stray fragments of neighbouring
artwork. This script deletes exactly those fragments (identified by connected component and bounding box, so
nothing else is touched), copies the assets into web/src/assets/brand under stable names, and generates
favicon/PWA icons. Requires Pillow and numpy.
"""
import sys
from collections import deque
from pathlib import Path

import numpy as np
from PIL import Image

SRC = Path(sys.argv[1]) if len(sys.argv) > 1 else sys.exit(__doc__)
ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "web" / "src" / "assets" / "brand"
PUBLIC = ROOT / "web" / "public"

# source file -> output name
NAMES = {
    "hawkeye_logo.png": "logo.png",                       # emblem + HawkEye wordmark (dark navy text: use on light backgrounds)
    "hawkeye.png": "emblem.png",
    "03_app_icon_square.png": "app-icon.png",             # has its own navy background: safe on light and dark
    "04_app_icon_round.png": "app-icon-round.png",
    "06_dashboard_laptop.png": "dashboard-laptop.png",    # illustrative artwork, not a real screenshot
    "07_cloud_server_security.png": "cloud-security.png",
    "08_website_globe_magnifier.png": "website-magnifier.png",
    "09_outage_alert_bell.png": "alert-bell.png",
    "11_history_icon.png": "icon-history.png",
    "12_analytics_icon.png": "icon-analytics.png",
    "13_security_shield_icon.png": "icon-shield.png",
    "14_email_alert_icon.png": "icon-email.png",
    "15_website_monitoring_icon.png": "icon-websites.png",
    "16_settings_icon.png": "icon-settings.png",
}
# 10_speedometer_icon.png is deliberately not used: the artwork is cropped flat at the bottom.
# 05_hawkeye_logo_compact.png duplicates hawkeye.png.

# Stray fragments to delete: file -> list of component bounding boxes (x0, y0, x1, y1) as measured on the originals.
STRAYS = {
    "06_dashboard_laptop.png": [(536, 200, 553, 288)],                                   # blue circle from the neighbouring crop
    "07_cloud_server_security.png": [(12, 328, 122, 371)],                               # corner of a laptop
    "08_website_globe_magnifier.png": [(325, 222, 346, 270), (307, 205, 339, 216), (316, 169, 344, 191)],  # bell pieces
    "09_outage_alert_bell.png": [(12, 282, 24, 296)],                                    # dark speck
}
THRESH = 24


def label(mask):
    h, w = mask.shape
    lab = np.zeros((h, w), dtype=np.int32)
    comps = []
    n = 0
    for y in range(h):
        for x in range(w):
            if mask[y, x] and not lab[y, x]:
                n += 1
                q = deque([(y, x)])
                lab[y, x] = n
                xs, ys = [], []
                while q:
                    cy, cx = q.popleft()
                    ys.append(cy)
                    xs.append(cx)
                    for dy in (-1, 0, 1):
                        for dx in (-1, 0, 1):
                            ny, nx = cy + dy, cx + dx
                            if 0 <= ny < h and 0 <= nx < w and mask[ny, nx] and not lab[ny, nx]:
                                lab[ny, nx] = n
                                q.append((ny, nx))
                comps.append((n, min(xs), min(ys), max(xs), max(ys)))
    return lab, comps


def dilate(mask, r):
    out = mask.copy()
    for dy in range(-r, r + 1):
        for dx in range(-r, r + 1):
            out |= np.roll(np.roll(mask, dy, 0), dx, 1)
    return out


def remove_strays(im, boxes, name):
    rgba = np.array(im.convert("RGBA"))
    lab, comps = label(rgba[:, :, 3] > THRESH)
    remove = np.zeros(lab.shape, dtype=bool)
    for box in boxes:
        match = [c for c in comps if (c[1], c[2], c[3], c[4]) == box]
        if len(match) != 1:
            sys.exit(f"{name}: expected one component at {box}; the source pack differs from the one this script was written for")
        remove |= lab == match[0][0]
    keep = (lab > 0) & ~remove
    # Also clear the soft anti-aliased fringe around removed pieces that does not touch anything we keep.
    fringe = dilate(remove, 3) & ~dilate(keep, 3)
    rgba[remove | fringe, 3] = 0
    return Image.fromarray(rgba)


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    for src, dst in NAMES.items():
        im = Image.open(SRC / src).convert("RGBA")
        if src in STRAYS:
            im = remove_strays(im, STRAYS[src], src)
        im.save(OUT / dst, optimize=True)
        print(f"{src:34s} -> {dst}  {im.size}")

    # Icons for browsers and home screens: the square app icon, padded to a square canvas.
    icon = Image.open(SRC / "03_app_icon_square.png").convert("RGBA")
    side = max(icon.size)
    canvas = Image.new("RGBA", (side, side), (0, 0, 0, 0))
    canvas.alpha_composite(icon, ((side - icon.width) // 2, (side - icon.height) // 2))
    for size, name in [(32, "favicon-32.png"), (180, "apple-touch-icon.png"), (192, "icon-192.png")]:
        canvas.resize((size, size), Image.LANCZOS).save(PUBLIC / name, optimize=True)
        print(f"favicon set -> public/{name}")


main()
