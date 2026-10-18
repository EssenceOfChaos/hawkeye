#!/usr/bin/env python3
"""Turn the hand-cropped glossy gold-ringed icon badges into the site's icon PNGs.

    python3 scripts/prepare_glossy_icons.py [web/src/assets/brand/source/icons]

The folder holds ten round badges on transparent backgrounds, roughly 360px each, cropped by hand from the ChatGPT icon sheet (an automatic cut-out
left a white halo outside the gold ring; a careful manual crop does not). The crops differ by a few pixels in size and position, so each badge is
centred on its own bounding box, placed on a square canvas and resized to 256x256, which keeps them identical in size on the page.
Writes web/src/assets/brand/icon-*.png (the keys of brand.icons in web/src/brand.ts). Needs Pillow and numpy.
"""
import sys
from pathlib import Path

import numpy as np
from PIL import Image

BRAND = Path(__file__).resolve().parent.parent / "web/src/assets/brand"
OUT = 256
NAMES = {  # source file -> output name
    "world_search_icon": "icon-websites",
    "mail_icon": "icon-email",
    "growth_icon": "icon-analytics",
    "history_icon": "icon-history",
    "cog_icon": "icon-settings",
    "shield_icon": "icon-shield",
    "bell_icon": "icon-notifications",
    "lock_cloud_icon": "icon-ssl",
    "laptop_icon": "icon-dashboard",
    "calendar_wrench_icon": "icon-maintenance",
}


def main() -> None:
    src = Path(sys.argv[1]) if len(sys.argv) > 1 else BRAND / "source/icons"
    for stem, name in NAMES.items():
        img = Image.open(src / f"{stem}.png").convert("RGBA")
        ys, xs = np.where(np.array(img.getchannel("A")) > 8)
        box = (xs.min(), ys.min(), xs.max() + 1, ys.max() + 1)
        badge = img.crop(box)
        side = max(badge.size)
        if not 300 <= side <= 420:
            sys.exit(f"{stem}: badge is {badge.size}, expected about 360px; was it cropped differently?")
        square = Image.new("RGBA", (side, side), (0, 0, 0, 0))
        square.alpha_composite(badge, ((side - badge.width) // 2, (side - badge.height) // 2))
        square.resize((OUT, OUT), Image.LANCZOS).save(BRAND / f"{name}.png", optimize=True)
        print(f"{name}: {badge.size} -> {OUT}x{OUT}")


if __name__ == "__main__":
    main()
