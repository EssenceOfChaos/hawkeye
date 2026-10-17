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
if you regenerate the art at another size, update the size check and the crop rows at the top. The in-app wordmark is HTML text with a
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
