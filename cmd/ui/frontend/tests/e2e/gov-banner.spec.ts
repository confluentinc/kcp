import { test, expect } from '@playwright/test'

test.describe('GOV-mode banner', () => {
  // The Playwright harness builds the default (prod) binary, so /edition
  // returns "prod" and the banner must stay hidden. The gov-build case (banner
  // shown) is covered by the Go endpoint test under `-tags=gov` and the manual
  // `make build-gov` check, since the harness cannot build a gov binary.
  test('is hidden on the prod build', async ({ page }) => {
    await page.goto('/')
    // Wait for the app to settle (pre-loaded state renders).
    await page.waitForSelector('text=AWS MSK', { timeout: 10000 })
    await expect(page.getByTestId('gov-banner')).toHaveCount(0)
  })

  test('/edition endpoint reports prod', async ({ request }) => {
    const res = await request.get('/edition')
    expect(res.ok()).toBeTruthy()
    expect(await res.json()).toEqual({ mode: 'prod' })
  })

  // The harness only builds the prod binary, so to exercise the banner's
  // positive (gov) render path we stub the /edition response. This covers the
  // `res.mode === 'gov'` branch in GovBanner that a prod build never hits.
  test('renders when /edition reports gov', async ({ page }) => {
    await page.route('**/edition', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ mode: 'gov' }),
      })
    )
    await page.goto('/')
    await expect(page.getByTestId('gov-banner')).toBeVisible()
    await expect(page.getByTestId('gov-banner')).toContainText('GOV MODE')
  })
})
