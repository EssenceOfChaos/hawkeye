// Captures the app's real screens for the landing page. Needs: the app running locally against a database seeded by seed.sql, with the dev server on :5199
// and the API on :8080 (worker OFF), and playwright-core with Google Chrome installed:  node scripts/screenshots/capture.mjs <output dir>
import { chromium } from 'playwright-core'
const out = process.argv[2]
const base = process.env.BASE ?? 'http://localhost:5199'
const browser = await chromium.launch({ channel: 'chrome' })
const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2, colorScheme: 'dark' })
const page = await ctx.newPage()
await page.goto(`${base}/login`, { waitUntil: 'networkidle' })
await page.getByLabel('Email').fill('owner@example.com')
await page.getByLabel('Password').fill('correct-horse-battery-sample')
await page.getByRole('button', { name: 'Log in' }).click()
await page.waitForURL('**/dashboard')
await page.waitForSelector('text=Average response time', { timeout: 15000 })
await page.waitForTimeout(1500)
await page.screenshot({ path: `${out}/dashboard.png`, fullPage: true })
await page.goto(`${base}/incidents`, { waitUntil: 'networkidle' })
await page.waitForTimeout(1200)
await page.screenshot({ path: `${out}/incidents.png`, fullPage: true })
await page.goto(`${base}/monitors`, { waitUntil: 'networkidle' })
await page.waitForTimeout(1200)
await page.screenshot({ path: `${out}/monitors.png`, fullPage: true })
await browser.close()
console.log('captured')
