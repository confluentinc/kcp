import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json' with { type: 'json' }

test.describe('State Loading Methods', () => {
  test('pre-loaded state via --state-file flag loads automatically', async ({ page }) => {
    await page.goto('/')
    // State is pre-loaded via --state-file flag in Playwright config
    // Clusters should appear without any upload action
    await page.waitForSelector('text=AWS MSK', { timeout: 10000 })
    await expect(page.locator('text=kcp-playground')).toBeVisible()
    await expect(page.locator('text=OPEN SOURCE KAFKA')).toBeVisible()
    await expect(page.locator('text=production-kafka-us-east')).toBeVisible()
  })

  test('upload button loads state and replaces pre-loaded data', async ({ page }) => {
    await page.goto('/')
    // Wait for pre-loaded state first
    await page.waitForSelector('text=AWS MSK', { timeout: 10000 })

    // Now upload OSK-only state via upload button
    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-osk-only.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateOSKOnly)),
    })

    // Wait for the new state to load
    await page.waitForSelector('text=OPEN SOURCE KAFKA', { timeout: 5000 })

    // OSK cluster from uploaded state should appear
    await expect(page.locator('text=prod-kafka-cluster')).toBeVisible()
  })
})
