# Branding assets

## Current logo and icons (glossy navy and gold)

These are final production assets, in `web/src/assets/brand/` (and `web/public/` for the favicons, apple-touch icon and email logo). Each was made once
with ChatGPT and then cut out, cropped and resized outside the repository. **The original renders and the one-off scripts that processed them are not kept**
(the one exception is `scripts/prepare_share_images.py`, below): the build and the deploy use only the results. Git history has them if they are ever needed
(`git log --diff-filter=D --name-only -- web/src/assets/brand/source 'scripts/prepare_*.py'`). What the files are, and the specs to match if one is replaced:

- **Logo:** `logo.png` (hawk + navy wordmark, for light backgrounds), `logo-white.png` (white wordmark, for dark backgrounds), `emblem.png` (hawk head alone, transparent),
  `app-icon.png` / `app-icon-round.png` (the gold-ringed badge), `public/{favicon-32,icon-192,apple-touch-icon}.png` (badge; the apple-touch icon sits on a navy square) and
  `public/email/hawkeye-logo.png`. The art is glossy and shaded, so it is PNG only (it turns muddy if traced to SVG). Backgrounds were removed by flood fill from the edges,
  so white feathers inside the artwork survive.
- **Feature and page icons** (`icon-websites`, `-email`, `-analytics`, `-history`, `-settings`, `-shield`, `-notifications`, `-ssl`, `-dashboard`, `-maintenance`, `-billing`, `-team`, `-status-page`, `-reports`, `-monitors`, `-developers`): glossy
  gold-ringed navy round badges, 256x256, transparent background, each centered on its own bounding box so all read at the same size. The Billing, Team, Status page, Reports and Monitors badges (`-billing`, `-team`, `-status-page`, `-reports`, `-monitors`) came from larger originals: isolated specks outside the ring and pixels of alpha 12 or less were removed, then they were centered and resized to 256x256. Each app page heading has its own badge (Domains uses `-websites`, Account `-settings`). `-developers` (a terminal prompt, on the landing page's "Works with Claude" card) came from a 1254x1254 original with no transparency: its black corners were masked off with an anti-aliased circle just inside the gold ring, then it was resized to 256x256. They were hand-cropped (an automatic
  cut-out left a white halo outside the ring). Each badge carries its own navy, so it works on any background.
- **Audience icons** (`audience-freelancer`, `-agency`, `-business`: one person, a group, a storefront): free-standing gold-edged navy glyphs, 256x256, transparent
  background, trimmed to their outline and centered with a 6% margin so the three read at a similar size (about 70 KB each; the originals were about 1 MB). Near-transparent
  pixels (alpha 12 or less) were removed: they show as colored specks against pure black and as nothing on the page.
- **`dashboard-laptop.webp`:** the hero laptop on the landing page, 1100px wide lossy WebP with alpha (145 KB). It is an illustration (alt text says so): its sidebar
  labels (Reports, Settings) and "Online" wording are not the app's exact ones.
- **Scout, the mascot** (`scout-wave`, `-check`, `-stand`, `-alert` `.webp`): a navy and gold hawk with a harness, a heartbeat shield badge and a cloud/code
  device on his back. The originals are 1254x1254 transparent PNGs (about 1.7 MB each), kept outside the repository by the product owner. Each was trimmed to its
  outline, centered on a square with his feet on the bottom edge, resized to 640x640 and saved as lossy WebP with alpha (70 to 95 KB). Render him with
  `components/Scout.tsx`, which adds a soft gold glow behind him (his navy feathers otherwise sink into the navy page) and hides him from screen readers
  unless given alt text. Poses: **wave** (greeting: the Monitors empty state), **check** (thumbs-up with a green check: not used since the hero animates),
  **stand** (neutral, gesturing with one wing: beside the 404 radar), **alert** (holding a red warning beacon: not used yet). Where he goes: personality
  moments (empty states, first-run screens, errors, marketing) and the emails below. Where he doesn't: dense working screens, customers' public status pages
  (their brand, not ours), and chat and webhook alerts. Keep the red **alert** pose off the 404 page and anything that could be read as a claim about system
  health. He is too detailed to read below about 64 px, so favicons and small icons stay with the logo badge.
- **Scout in motion** (`scout-rest.webp`, `scout-frames.webp`): the landing hero's Scout, in front of the laptop, rendered with
  `components/AnimatedScout.tsx`. He waves hello once the page has loaded and gives a thumbs-up with a green check, then blinks every few seconds (every
  fourth time, a thumbs-up instead), and flaps his wings with a small hop when pointed at or tapped. The frames come from eleven 1254x1254 originals of
  one pose (rest, blink, three wave steps, thumbs-up, and five steps of raising his wings, from just lifting off his body to raised high), also kept by
  the product owner. A flap plays the five wing steps up, down and up again, one step at a time, so the wings never jump. Most originals had him drawn
  up to 64 px left of the rest frame, and some of the wing steps a few percent larger, so each was shifted (and, for the wing steps, scaled) until his
  face, badge and feet lined up with the rest frame, then placed on a 1290x1290 square wide enough for spread wings, resized to 400x400 and joined left
  to right into one strip: lossy WebP with alpha, 324 KB. `scout-rest.webp` is the first frame alone (29 KB): it shows first, the strip loads only after
  the page has, and people who prefer reduced motion keep the still and never download the strip. New frames must line up the same way, or he jitters
  as they change.
- **Scout in emails** (`web/public/email/scout-welcome.png`, `-monitor-down.png`, `-test-alert.png`, served at `/email/` like the logo): set with
  `email.Content.Picture`, which shows him at 112 px beside the heading, HTML only and with empty alt text (the text says everything). **Welcome** (waving)
  is on the "Confirm your email address" email, the first one a new account gets. **Monitor down** (worried, holding an envelope with a red warning) is on
  every outage alert: websites, DNS checks and cron jobs; not on recoveries, slowdowns or certificate and domain warnings. He sits beside the heading so
  the facts stay as high as before. **Test alert** (thumbs-up, holding an envelope with a green check) is on the "Your alerts are working" test email. The
  originals are 1254x1254 transparent PNGs kept by the product owner; each was trimmed to his outline, placed on a 240x240 square with his feet at the
  bottom, given a soft gold glow behind him (email clients can't draw the web's CSS glow, and his navy feathers sink into the navy card without it) and
  saved as full-color PNG with alpha (90 to 96 KB). PNG, not WebP, because Outlook can't show WebP; full color because 256 colors lost the glow.
- **`monitoring-hawk-eye.webp`, `monitoring-earth-network.webp`:** the /monitoring page's background art (a gold hawk's eye on navy; the night Earth with connection arcs),
  lossy WebP at full width. No text in either. They are photographic gradients, so WebP is a fraction of a PNG's size with no visible banding.

The old blue icons, `alert-bell.png`, `cloud-security.png`, `website-magnifier.png` and `dashboard-laptop.png` were removed.

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
gold "Eye". The brand blue is still the UI accent color; gold appears in the logo, the app wordmark and the Pro plan's aura.

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

## Link-preview images

`web/public/share/<name>.jpg` (1200x630) are what link previews (Slack, X, LinkedIn, iMessage) show for the public pages. The page's text (set in Sora and Inter over a left-hand shade) and the white logo are baked into each JPEG, over the earth, hawk's eye and laptop artwork. **`scripts/prepare_share_images.py` is kept** because, unlike the other assets, these get redone: a reworded page, or a new page with its own preview. It is not needed to build or deploy (the JPEGs are committed). Its text-free background (1774x887, 2 MB) is not kept in the repository, so the script takes it as an argument; get it from git history with `git show 89f5585^:web/src/assets/brand/source/share-background.png > /tmp/share-background.png`, then `python3 scripts/prepare_share_images.py /tmp/share-background.png` (needs Pillow, Google Chrome, and `make web-install` for the fonts; add `--out <dir>` to try a change without overwriting the live images). To add or reword one, edit `IMAGES` in the script, run it, and name the image in the page's entry in `web/src/seo.ts`. Run on the current script it reproduces the committed JPEGs exactly. The laptop screen in the background is generic artwork (it is not a screenshot of HawkEye), like the landing hero.

## Cleanup

Four crops contained fragments of neighboring artwork (a blue circle beside the laptop, a laptop corner under the cloud server,
bell pieces beside the magnifier, a speck on the bell). were removed once, when the pack was first prepared (the script that did it is no longer kept).
The laptop image is cut off at its right edge in the original; the landing page fades that edge with a CSS mask.

## Theme and resolution

- Marketing page: always light (`.landing` forces `color-scheme: light`) because several assets are dark navy on transparency.
- App: follows the OS theme; the brand mark is the app icon plus HTML text so it adapts.
- Source images are small (icons ≈ 200 px, laptop 566 px). They are displayed at or below 1x–2x of their pixel size; if the design is
  extended to larger or print uses, request SVG or higher-resolution originals.
