# Branding assets

## Current logo (glossy navy and gold)

Sources: the three PNGs in `web/src/assets/brand/source/` (`HawkEye Logo Colorway Showcase.png`, `Glossy Golden Hawk Emblem.png`,
`Glossy Gold-Ringed Hawk Emblem.png`), generated with ChatGPT from the earlier flat logo sheet (`hawkeye-brand-logo-sheet.png`, kept there too).

`scripts/prepare_glossy_logos.py "<folder with the three PNGs>"` (needs Pillow and numpy) removes the flat backgrounds by flood fill from
the edges (so white feathers survive), feathers the edge, clears the letter counters, and writes:

- `logo.png` (hawk + navy wordmark, for light backgrounds) and `logo-white.png` (white wordmark, for dark backgrounds, not used yet),
- `emblem.png` (hawk head alone, transparent), `app-icon.png` / `app-icon-round.png` (the gold-ringed badge),
- `public/{favicon-32,icon-192,apple-touch-icon}.png` (badge; the apple-touch icon sits on a navy square) and `public/email/hawkeye-logo.png`.

The art is glossy and shaded, so it is PNG only (it turns muddy if traced to SVG). The script refuses to run if the image sizes differ;
if you regenerate the art at another size, update the size check and the crop rows at the top. **Feature and page icons** (`icon-websites`, `-email`, `-analytics`, `-history`, `-settings`, `-shield`, `-notifications`, `-ssl`, `-dashboard`, `-maintenance`) are
glossy gold-ringed navy badges from the ChatGPT icon sheet, hand-cropped (an automatic cut-out left a white halo outside the ring). The originals are in
`web/src/assets/brand/source/icons/`; `python3 scripts/prepare_glossy_icons.py` (Pillow and numpy) centres each on a square canvas and writes the 256px PNGs.
Each badge carries its own navy, so it works on any background. The old blue icons, `alert-bell.png`, `cloud-security.png` and `website-magnifier.png` were removed;
`dashboard-laptop.png` was replaced by `dashboard-laptop.webp`, the hero laptop on the landing page: `python3 scripts/prepare_hero_laptop.py` resizes the glossy mockup in `source/dashboard-laptop-mockup.png` to 1100px wide lossy WebP with alpha (145 KB). It is an illustration (alt text says so): its sidebar labels (Reports, Settings) and "Online" wording are not the app's exact ones.

**Screenshots** (the landing page's "See it in action" section: `web/src/assets/brand/screens/{dashboard,incidents,alert-email}.webp`) are real captures of the app with
**sample data**, so they stay honest. To regenerate after the UI or the emails change (all local, nothing near production):
1. Start a throwaway Postgres, run the migrations, start the API (`go run ./cmd/api`) and the web dev server (`npm run dev`), **without the worker**.
2. Register `owner@example.com` / `correct-horse-battery-sample` in the app, then seed it: `psql ... -v org=<that organization's id> -f scripts/screenshots/seed.sql`
   (five fictional monitors on example.com, 24 hours of one-minute checks, one 14-minute outage that was confirmed and recovered, a few older incidents; it can be re-run).
3. `node scripts/screenshots/capture.mjs <dir>` (needs `playwright-core` and Google Chrome) logs in through the real login page and captures the dashboard and the incidents list at 2x.
4. The alert email comes from the real renderer: `ALERT_PREVIEW_DIR=<dir> ALERT_PREVIEW_BASE=https://hawkeye.observer go test ./internal/notifications -run TestWriteAlertPreview`,
   then screenshot `alert-outage.html` at 640 px wide, 2x (serve `web/public/email/hawkeye-logo.png` for its logo so the image never depends on what is deployed).
5. `python3 scripts/screenshots/process.py <dir>` crops and compresses them into the `screens/` folder (about 190 KB in total).
Every image's alt text says "sample data"; keep that if you change them.

The in-app wordmark is HTML text with a
gold "Eye". The brand blue is still the UI accent colour; gold appears in the logo, the app wordmark and the Pro plan's aura.

The rest of this file describes the earlier blue brand pack, whose icons (analytics, email, shield, etc.) are still used.

The brand pack (`hawkeye_transparent_assets`, supplied by the product owner) is a set of PNG crops of a generated concept sheet.
Its own README warns that fine details may be inexact and recommends recreating the logo as SVG and checking trademark availability
for the name "HawkEye" before launch. **Both are still open items.**

## What is used where

| Asset (output name) | Source file | Used for |
|---|---|---|
| `app-icon.png`, favicons | *(replaced by the logo sheet, see above)* | app bar mark, auth pages, favicon, apple-touch icon (has its own navy background, so it is legible in light and dark themes) |
| `logo.png` | *(replaced by the logo sheet, see above)* | landing page header only (the wordmark is dark navy and is invisible on dark backgrounds) |
| `dashboard-laptop.png` | `06_dashboard_laptop.png` | landing hero. It is **illustrative artwork, not a screenshot**; alt text says so |
| `icon-websites/email/history/settings/shield/analytics.png` | `15, 14, 11, 16, 13, 12` | landing feature cards; website, email and bell icons also head the Monitors, Alerts and Incidents pages |
| `website-magnifier.png`, `alert-bell.png`, `cloud-security.png` | `08, 09, 07` | landing page sections |

Not used: `10_speedometer_icon.png` (its artwork is cropped flat at the bottom), `05_hawkeye_logo_compact.png` and `hawkeye.png`
(duplicates of the emblem in the full logo), `04_app_icon_round.png` (kept in the repo for later use).

## Cleanup

Four crops contained fragments of neighbouring artwork (a blue circle beside the laptop, a laptop corner under the cloud server,
bell pieces beside the magnifier, a speck on the bell). `scripts/prepare_brand_assets.py /path/to/pack` removes exactly those
connected components and fails loudly if the pack differs from the one it was written for. It also regenerates the favicons.
The laptop image is cut off at its right edge in the original; the landing page fades that edge with a CSS mask.

## Theme and resolution

- Marketing page: always light (`.landing` forces `color-scheme: light`) because several assets are dark navy on transparency.
- App: follows the OS theme; the brand mark is the app icon plus HTML text so it adapts.
- Source images are small (icons ≈ 200 px, laptop 566 px). They are displayed at or below 1x–2x of their pixel size; if the design is
  extended to larger or print uses, request SVG or higher-resolution originals.
