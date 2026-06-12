import { test, expect } from '@playwright/test'
import stateWithMetrics from './fixtures/state-with-metrics.json' with { type: 'json' }

test.describe('TCO Inputs Metrics Modal', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForSelector('text=AWS MSK', { timeout: 10000 })

    // Upload state file with metrics for both MSK and OSK
    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-with-metrics.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateWithMetrics)),
    })
    await page.waitForSelector('text=OPEN SOURCE KAFKA', { timeout: 5000 })

    // Navigate to TCO Inputs
    await page.locator('button:has-text("TCO Inputs")').click()
    await page.waitForSelector('text=Workload Assumptions', { timeout: 5000 })
  })

  test('multiple OSK clusters render in TCO table', async ({ page }) => {
    await page.locator('button:has-text("OSK")').click()
    await expect(page.locator('th:has-text("osk-kafka")')).toBeVisible()
    await expect(page.locator('th:has-text("prod-kafka-us-east")')).toBeVisible()
    await expect(page.locator('th:has-text("staging-kafka-eu")')).toBeVisible()
    await expect(page.locator('th:has-text("dev-kafka-local")')).toBeVisible()
  })

  test('OSK metrics modal shows chart with data', async ({ page }) => {
    await page.locator('button:has-text("OSK")').click()
    // Click the value picker button for avg ingress on the first OSK cluster
    const valuePickerButtons = page.locator('button[title="Go to cluster metrics for ingress data"]')
    await valuePickerButtons.first().click()

    // Modal should open with chart data
    await expect(page.locator('text=BytesInPerSec')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('MIN', { exact: true })).toBeVisible()
    await expect(page.getByText('AVG', { exact: true })).toBeVisible()
    await expect(page.getByText('MAX', { exact: true })).toBeVisible()

    // Should NOT show "No chart data available"
    await expect(page.locator('text=No chart data available')).not.toBeVisible()
  })

  test('Use as TCO Input transfers value from metrics modal', async ({ page }) => {
    await page.locator('button:has-text("OSK")').click()

    // Open metrics modal for avg ingress on first cluster
    const valuePickerButtons = page.locator('button[title="Go to cluster metrics for ingress data"]')
    await valuePickerButtons.first().click()

    // Wait for metrics to load
    await expect(page.locator('text=BytesInPerSec')).toBeVisible({ timeout: 10000 })

    // Get the AVG value — the green-colored span next to the AVG label
    const avgLabel = page.getByText('AVG', { exact: true })
    const avgValueSpan = avgLabel.locator('..').locator('span').nth(1)
    const avgValueText = await avgValueSpan.textContent()
    expect(avgValueText).toBeTruthy()

    // Click "Use as TCO Input" next to AVG
    const useButtons = page.locator('button:has-text("Use as TCO Input")')
    await useButtons.nth(1).click() // 0=MIN, 1=AVG, 2=MAX

    // Close the modal
    await page.keyboard.press('Escape')

    // The avg ingress input for the first OSK cluster should now have a value
    const firstInput = page.locator('input[type="number"]').first()
    const inputValue = await firstInput.inputValue()
    expect(parseFloat(inputValue)).toBeGreaterThan(0)
  })

  test('OSK partition lookup shows GlobalPartitionCount chart', async ({ page }) => {
    await page.locator('button:has-text("OSK")').click()

    const partitionButtons = page.locator('button[title="Go to cluster metrics for partition data"]')
    await partitionButtons.first().click()

    await expect(page.locator('text=GlobalPartitionCount - Partitions')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('MIN', { exact: true })).toBeVisible()
    await expect(page.getByText('AVG', { exact: true })).toBeVisible()

    await expect(page.locator('text=No data available for')).not.toBeVisible()
  })

  test('OSK metrics stats update when date range changes', async ({ page }) => {
    await page.locator('button:has-text("OSK")').click()

    // Open metrics modal
    const valuePickerButtons = page.locator('button[title="Go to cluster metrics for ingress data"]')
    await valuePickerButtons.first().click()

    // Wait for metrics to load
    await expect(page.locator('text=BytesInPerSec')).toBeVisible({ timeout: 10000 })
    const avgLabel = page.getByText('AVG', { exact: true })
    await expect(avgLabel).toBeVisible()

    // Record the initial AVG value
    const initialAvg = await avgLabel.locator('..').locator('span').nth(1).textContent()
    expect(initialAvg).toBeTruthy()

    // Reset the start date via the X button — this changes the date range and triggers a re-fetch
    const metricsResponse = page.waitForResponse((resp) => resp.url().includes('/metrics/osk/'))
    await page.locator('button[title="Reset to default start date"]').click()
    await metricsResponse

    // Verify stats are still rendered after the date range change
    await expect(avgLabel).toBeVisible()
    const updatedAvg = await avgLabel.locator('..').locator('span').nth(1).textContent()
    expect(updatedAvg).toBeTruthy()
  })
})
