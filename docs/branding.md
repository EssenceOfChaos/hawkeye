# Branding assets

The brand pack (`hawkeye_transparent_assets`, supplied by the product owner) is a set of PNG crops of a generated concept sheet.
Its own README warns that fine details may be inexact and recommends recreating the logo as SVG and checking trademark availability
for the name "HawkEye" before launch. **Both are still open items.**

## What is used where

| Asset (output name) | Source file | Used for |
|---|---|---|
| `app-icon.png`, favicons | `03_app_icon_square.png` | app bar mark, auth pages, favicon, apple-touch icon (has its own navy background, so it is legible in light and dark themes) |
| `logo.png` | `hawkeye_logo.png` | landing page header only (the wordmark is dark navy and is invisible on dark backgrounds) |
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
