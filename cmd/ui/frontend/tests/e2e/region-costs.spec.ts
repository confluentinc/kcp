import { test, expect } from '@playwright/test'
import stateWithMetrics from './fixtures/state-with-metrics.json' with { type: 'json' }

test.describe('Region Costs', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForSelector('text=AWS MSK', { timeout: 10000 })

    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    // Wait for THIS upload's own /upload-state response rather than the generic
    // "Apache Kafka" text: that text is also satisfied by the server's
    // independently preloaded default state file (see playwright.config.ts's
    // webServer --state-file), which loads on mount regardless of this test's
    // upload. Waiting on it alone races against this fixture's own upload
    // finishing — whichever settles last silently wins the app's selected view,
    // so a click right after can be reverted moments later when the slower one
    // resolves.
    const uploadResponse = page.waitForResponse(
      (res) => res.url().includes('/upload-state') && res.request().method() === 'POST'
    )
    await fileInput.setInputFiles({
      name: 'state-with-metrics.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateWithMetrics)),
    })
    await uploadResponse
    // eu-south-1 exists only in this fixture (not the preloaded default state),
    // so its presence confirms this upload — not the preloaded one — has rendered.
    await expect(page.locator('button:has-text("eu-south-1")')).toBeVisible({ timeout: 5000 })
  })

  test('region cost overview renders with service and cost type selectors', async ({ page }) => {
    await page.locator('button:has-text("us-east-1")').click()
    await expect(page.locator('text=Region Cost Overview')).toBeVisible({ timeout: 5000 })

    // Service and Cost Type labels should be visible
    await expect(page.getByText('Service', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('Cost Type', { exact: true })).toBeVisible()
  })

  test('cost type selector shows available options', async ({ page }) => {
    await page.locator('button:has-text("us-east-1")').click()
    await expect(page.locator('text=Region Cost Overview')).toBeVisible({ timeout: 5000 })

    // Open cost type dropdown
    const costTypeSelect = page.locator('button:has-text("Unblended Cost")')
    await costTypeSelect.click()

    // All cost type options should be available
    await expect(page.getByText('Blended Cost', { exact: true })).toBeVisible()
    await expect(page.getByText('Amortized Cost', { exact: true })).toBeVisible()
    await expect(page.getByText('Net Amortized Cost', { exact: true })).toBeVisible()
    await expect(page.getByText('Net Unblended Cost', { exact: true })).toBeVisible()
  })

  test('cost data table tab renders with service data', async ({ page }) => {
    await page.locator('button:has-text("us-east-1")').click()
    await expect(page.locator('text=Region Cost Overview')).toBeVisible({ timeout: 5000 })

    // Switch to table tab
    const tableTab = page.locator('button:has-text("Table")')
    await tableTab.click()

    // Table should show cost data with service names. The Service selector above the
    // tabs also displays this same text (it now drives both the chart and the table,
    // per the shared-selector consolidation), so scope to the table cell specifically
    // (its title attribute) rather than a page-wide text match.
    await expect(page.getByTitle('Amazon Managed Streaming for Apache Kafka', { exact: true })).toBeVisible({
      timeout: 5000,
    })
  })
})
