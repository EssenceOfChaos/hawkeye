#!/usr/bin/env python3
"""Cut the site's logo assets out of the glossy navy-and-gold ChatGPT renders.

    python3 scripts/prepare_glossy_logos.py "/path/to/hawkeye improved logos"

The folder holds three PNGs on flat backgrounds (sources are kept in web/src/assets/brand/source/):

  * "HawkEye Logo Colorway Showcase.png" (2172 x 724): navy lockup on white (top), white lockup on navy (bottom), no tagline,
  * "Glossy Golden Hawk Emblem.png" (1254 x 1254): the hawk head on white,
  * "Glossy Gold-Ringed Hawk Emblem.png" (1254 x 1254): the round navy badge on white.

The flat background is removed by flood fill from the edges (so white feathers inside the artwork survive), the edge is eroded a pixel
and feathered so there is no halo, and the enclosed letter counters in the wordmark are cleared too.

Writes web/src/assets/brand/{logo,logo-white,emblem,app-icon,app-icon-round}.png, web/public/{favicon-32,apple-touch-icon,icon-192}.png
and web/public/email/hawkeye-logo.png. Needs Pillow and numpy.
"""
import sys
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFilter

ROOT = Path(__file__).resolve().parent.parent
BRAND = ROOT / "web/src/assets/brand"
PUBLIC = ROOT / "web/public"

SHOWCASE = "HawkEye Logo Colorway Showcase.png"
HAWK = "Glossy Golden Hawk Emblem.png"
BADGE = "Glossy Gold-Ringed Hawk Emblem.png"

LIGHT_ROWS = (0, 360)   # navy lockup on white
DARK_ROWS = (368, 724)  # white lockup on navy
WORDMARK_FROM_X = 860   # right of the hawk's beak; counters are only cleared from here on
NAVY = (11, 29, 58)     # fallback; the real value is sampled from the dark band


def flood_background(img: Image.Image, thresh: int) -> np.ndarray:
    """Boolean mask of the background: pixels connected to the image border whose colour is within `thresh` of the corner colour."""
    rgb = img.convert("RGB")
    sentinel = (255, 0, 255)
    probe = rgb.copy()
    w, h = probe.size
    for seed in [(0, 0), (w - 1, 0), (0, h - 1), (w - 1, h - 1), (w // 2, 0), (w // 2, h - 1), (0, h // 2), (w - 1, h // 2)]:
        if probe.getpixel(seed) != sentinel:
            ImageDraw.floodfill(probe, seed, sentinel, thresh=thresh)
    return np.all(np.array(probe) == sentinel, axis=2)


def cut_out(img: Image.Image, bg_mask: np.ndarray, feather: float = 0.8) -> Image.Image:
    """RGBA from a background mask: erode the foreground a pixel (drops the background-tinted fringe), then feather the edge."""
    fg = Image.fromarray(((~bg_mask) * 255).astype(np.uint8), "L")
    fg = fg.filter(ImageFilter.MinFilter(3)).filter(ImageFilter.GaussianBlur(feather))
    out = img.convert("RGBA")
    out.putalpha(fg)
    return out


def clear_counters(img: Image.Image, colour: tuple, tol: int, from_x: int) -> Image.Image:
    """Make enclosed pixels close to the background colour transparent inside the wordmark (letter counters). Only right of from_x."""
    a = np.array(img)
    dist = np.abs(a[:, :, :3].astype(int) - np.array(colour)).max(axis=2)
    hole = dist <= tol
    hole[:, :from_x] = False
    alpha = Image.fromarray(np.where(hole, 0, 255).astype(np.uint8), "L").filter(ImageFilter.GaussianBlur(0.6))
    a[:, :, 3] = np.minimum(a[:, :, 3], np.array(alpha))
    return Image.fromarray(a, "RGBA")


def trim(img: Image.Image, pad: int = 0) -> Image.Image:
    alpha = np.array(img)[:, :, 3]
    ys, xs = np.where(alpha > 8)
    return img.crop((max(0, xs.min() - pad), max(0, ys.min() - pad), min(img.width, xs.max() + 1 + pad), min(img.height, ys.max() + 1 + pad)))


def fit_width(img: Image.Image, width: int) -> Image.Image:
    return img.resize((width, round(img.height * width / img.width)), Image.LANCZOS)


def fit_box(img: Image.Image, size: int) -> Image.Image:
    scale = size / max(img.size)
    return img.resize((round(img.width * scale), round(img.height * scale)), Image.LANCZOS)


def on_square(img: Image.Image, size: int, fill=None, scale: float = 1.0) -> Image.Image:
    """Centre img on a size x size canvas (transparent, or `fill`), scaled to `scale` of the canvas."""
    canvas = Image.new("RGBA", (size, size), fill or (0, 0, 0, 0))
    inner = fit_box(img, round(size * scale))
    canvas.alpha_composite(inner, ((size - inner.width) // 2, (size - inner.height) // 2))
    return canvas


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    src = Path(sys.argv[1])
    sheet = Image.open(src / SHOWCASE).convert("RGB")
    hawk = Image.open(src / HAWK).convert("RGB")
    badge = Image.open(src / BADGE).convert("RGB")
    if sheet.size != (2172, 724) or hawk.size != (1254, 1254) or badge.size != (1254, 1254):
        sys.exit(f"unexpected sizes {sheet.size}, {hawk.size}, {badge.size}; this script was written for 2172x724 and 1254x1254")

    light = sheet.crop((0, LIGHT_ROWS[0], sheet.width, LIGHT_ROWS[1]))
    dark = sheet.crop((0, DARK_ROWS[0], sheet.width, DARK_ROWS[1]))
    navy = tuple(int(v) for v in np.array(dark)[8:24, 8:24].reshape(-1, 3).mean(axis=0))

    logo = cut_out(light, flood_background(light, 36))
    logo = clear_counters(logo, (255, 255, 255), 30, WORDMARK_FROM_X)
    logo = trim(logo, 6)
    logo_white = cut_out(dark, flood_background(dark, 22))
    logo_white = clear_counters(logo_white, navy, 14, WORDMARK_FROM_X)
    logo_white = trim(logo_white, 6)
    emblem = trim(cut_out(hawk, flood_background(hawk, 36)), 6)
    badge_rgba = trim(cut_out(badge, flood_background(badge, 36)), 2)

    fit_width(logo, 960).save(BRAND / "logo.png", optimize=True)
    fit_width(logo_white, 960).save(BRAND / "logo-white.png", optimize=True)
    fit_box(emblem, 640).save(BRAND / "emblem.png", optimize=True)
    on_square(badge_rgba, 256).save(BRAND / "app-icon.png", optimize=True)
    on_square(badge_rgba, 256).save(BRAND / "app-icon-round.png", optimize=True)
    on_square(badge_rgba, 32).save(PUBLIC / "favicon-32.png", optimize=True)
    on_square(badge_rgba, 192).save(PUBLIC / "icon-192.png", optimize=True)
    on_square(badge_rgba, 180, fill=(*navy, 255), scale=0.86).save(PUBLIC / "apple-touch-icon.png", optimize=True)  # iOS rounds it itself
    fit_width(logo_white, 480).save(PUBLIC / "email/hawkeye-logo.png", optimize=True)  # email clients need a PNG; the emails are navy, so the white-wordmark version
    print("logo", logo.size, "logo-white", logo_white.size, "emblem", emblem.size, "badge", badge_rgba.size, "navy", navy)


if __name__ == "__main__":
    main()
