import { test, expect } from '@playwright/test'
import stateWithMetrics from './fixtures/state-with-metrics.json' with { type: 'json' }

test.describe('Region Costs', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForSelector('text=AWS MSK', { timeout: 10000 })

    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-with-metrics.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateWithMetrics)),
    })
    await page.waitForSelector('text=OPEN SOURCE KAFKA', { timeout: 5000 })
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

    // Table should show cost data with service names
    await expect(page.locator('text=Amazon Managed Streaming for Apache Kafka')).toBeVisible({
      timeout: 5000,
    })
  })
})
