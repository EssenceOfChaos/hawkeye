#!/usr/bin/env python3
"""Make the link-preview images (Open Graph / Twitter cards) for the public pages.

    python3 scripts/prepare_share_images.py <share-background.png> [--out DIR]

Reads the background (1774x887, the earth, hawk's eye and laptop artwork, no text) and writes <name>.jpg at 1200x630, one per entry in IMAGES below, into
web/public/share (or DIR, to try a change without touching the live images). The text is typeset in headless Chrome with the site's own fonts (Sora and
Inter from web/node_modules, so run `make web-install` first), then saved as JPEG with Pillow. Chrome is found from $CHROME, the usual macOS location, or
the PATH. Needs Pillow.

The background is NOT kept in the repository (only the finished JPEGs are needed to build and run the site), but it is in git history. Get it with

    git show 89f5585^:web/src/assets/brand/source/share-background.png > /tmp/share-background.png

(if that commit is ever gone: `git log --diff-filter=D --name-only -- web/src/assets/brand/source/share-background.png` names the commit that deleted it,
and its parent has the file).

To add or reword an image, edit IMAGES and run this again; point the page's `image` in web/src/seo.ts at /share/<name>.jpg. Pages without
their own image use home.jpg (web/src/seo.ts, DEFAULT_SHARE_IMAGE).
"""
import argparse
import html
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
LOGO = ROOT / "web/src/assets/brand/logo-white.png"
FONTS = ROOT / "web/node_modules/@fontsource-variable"
DEFAULT_OUT = ROOT / "web/public/share"

# name: (eyebrow, title with <em> for the gold part and <br> for line breaks, subtitle[, title size in px (default 56)])
IMAGES = {
    "home": ("Website &amp; API monitoring", "Know Before<br><em>Your Customers Do.</em>", "Uptime checks, instant alerts, and SSL and domain expiry warnings.", 46),
    "tools": ("Free tools", "Free Website<br>Monitoring <em>Tools</em>", "Check certificates, plan uptime and estimate what downtime costs.", 50),
    "is-website-down": ("Free tool", "Is My Website<br><em>Down?</em>", "Check if a site is up, with its status code and response time."),
    "ssl-certificate-checker": ("Free tool", "SSL Certificate<br><em>Checker</em>", "See when a certificate expires, who issued it and whether browsers trust it."),
    "uptime-sla-calculator": ("Free tool", "Uptime SLA<br><em>Calculator</em>", "How much downtime does 99.9% allow? Per day, week, month and year."),
    "downtime-cost-calculator": ("Free tool", "Downtime Cost<br><em>Calculator</em>", "Estimate the revenue a website outage puts at risk."),
}

PAGE = """<!doctype html><meta charset="utf-8"><style>
@font-face {{ font-family: Sora; font-weight: 100 800; src: url("{sora}") format("woff2"); }}
@font-face {{ font-family: Inter; font-weight: 100 900; src: url("{inter}") format("woff2"); }}
* {{ box-sizing: border-box; }}
body {{ margin: 0; width: 1200px; height: 630px; position: relative; overflow: hidden; background: #071426; font-family: Inter, sans-serif; color: #fff; }}
.bg {{ position: absolute; inset: 0; background: url("{bg}") right center / auto 100% no-repeat; }}
.shade {{ position: absolute; inset: 0; background: linear-gradient(90deg, #071426f5 0%, #071426e6 36%, #07142680 52%, #07142600 66%); }}
.content {{ position: absolute; left: 64px; top: 52px; bottom: 52px; width: 480px; display: flex; flex-direction: column; }}
.logo {{ height: 46px; width: auto; align-self: flex-start; }}
.spacer {{ flex: 1; }}
.eyebrow {{ font-weight: 600; font-size: 20px; letter-spacing: .14em; text-transform: uppercase; color: #e8b95e; margin-bottom: 14px; }}
h1 {{ font-family: Sora, sans-serif; font-weight: 700; font-size: {size}px; line-height: 1.08; letter-spacing: -.02em; margin: 0 0 20px; }}
h1 em {{ font-style: normal; color: #f4d18d; }}
p {{ font-weight: 500; font-size: 25px; line-height: 1.35; color: #c9d6e6; margin: 0 0 26px; }}
.url {{ font-weight: 600; font-size: 21px; color: #e8b95e; }}
</style>
<div class="bg"></div><div class="shade"></div>
<div class="content">
  <img class="logo" src="{logo}" alt="">
  <div class="spacer"></div>
  <div class="eyebrow">{eyebrow}</div>
  <h1>{title}</h1>
  <p>{sub}</p>
  <div class="url">hawkeye.observer</div>
</div>
"""


def find_chrome() -> str:
    candidates = [os.environ.get("CHROME", ""), "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"]
    candidates += [shutil.which(n) or "" for n in ("google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome")]
    for c in candidates:
        if c and Path(c).exists():
            return c
    raise SystemExit("Chrome not found: set CHROME to its path")


def font(family: str) -> str:
    path = FONTS / family / "files" / f"{family}-latin-wght-normal.woff2"
    if not path.exists():
        raise SystemExit(f"{path} not found: run `make web-install` first")
    return path.as_uri()


def main() -> None:
    ap = argparse.ArgumentParser(description="Make the link-preview images.")
    ap.add_argument("background", type=Path, help="share-background.png (1774x887, no text); see the top of this file for where to get it")
    ap.add_argument("--out", type=Path, default=DEFAULT_OUT, help="where to write the JPEGs (default: web/public/share)")
    args = ap.parse_args()
    BG, OUT = args.background.resolve(), args.out.resolve()
    if not BG.exists():
        raise SystemExit(f"{BG} not found: see the top of this file for how to get the background out of git history")
    if Image.open(BG).size != (1774, 887):
        raise SystemExit(f"{BG.name}: unexpected size; this script was written for 1774x887")
    chrome = find_chrome()
    OUT.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        for name, (eyebrow, title, sub, *size) in IMAGES.items():
            page, shot = Path(tmp) / f"{name}.html", Path(tmp) / f"{name}.png"
            page.write_text(PAGE.format(sora=font("sora"), inter=font("inter"), bg=BG.as_uri(), logo=LOGO.as_uri(),
                                        eyebrow=eyebrow, title=title, size=size[0] if size else 56, sub=html.escape(sub, quote=False)), encoding="utf-8")
            subprocess.run([chrome, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--force-device-scale-factor=1", "--window-size=1200,630",
                            "--allow-file-access-from-files", "--virtual-time-budget=4000", f"--screenshot={shot}", page.as_uri()],
                           check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            img = Image.open(shot).convert("RGB")
            if img.size != (1200, 630):
                raise SystemExit(f"{name}: Chrome made a {img.size} image, expected 1200x630")
            out = OUT / f"{name}.jpg"
            img.save(out, quality=86, optimize=True, progressive=True)
            print(out.relative_to(ROOT) if out.is_relative_to(ROOT) else out, img.size, out.stat().st_size, "bytes")


if __name__ == "__main__":
    sys.exit(main())
