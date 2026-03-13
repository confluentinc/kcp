import { test, expect } from '@playwright/test'
import stateBoth from '../fixtures/state-both.json' with { type: 'json' }

test.describe('Switching Between MSK and OSK', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

    // Click the upload button to reveal the file input
    await page.click('button:has-text("Upload KCP State File")')

    // Upload state with both MSK and OSK
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-both.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateBoth)),
    })

    // Wait for both sections to appear (confirms state is loaded)
    await page.waitForSelector('text=AWS MSK', { timeout: 5000 })
    await page.waitForSelector('text=OPEN SOURCE KAFKA', { timeout: 5000 })
  })

  test('displays both MSK and OSK sections', async ({ page }) => {
    await expect(page.locator('text=AWS MSK')).toBeVisible()
    await expect(page.locator('text=OPEN SOURCE KAFKA')).toBeVisible()
  })

  test('can switch from MSK cluster to OSK cluster', async ({ page }) => {
    // Click MSK cluster
    await page.click('text=msk-cluster-1')
    await expect(
      page.locator('text=arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster-1')
    ).toBeVisible()

    // Click OSK cluster
    await page.click('text=staging-kafka-cluster')
    await expect(page.locator('text=Bootstrap Servers')).toBeVisible()

    // Verify ARN is no longer visible
    await expect(
      page.locator('text=arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster-1')
    ).not.toBeVisible()
  })

  test('MSK summary only shows MSK data', async ({ page }) => {
    // Click Summary button and wait for content to load
    await page.click('button:has-text("Summary")')

    // Wait for the Summary view to render (shows "MSK Cost Summary" when data is present)
    await page.waitForSelector('h1:has-text("MSK Cost Summary")', { timeout: 5000 })

    // Verify MSK cost summary appears (MSK-only feature)
    await expect(page.locator('text=MSK Cost Summary')).toBeVisible()
  })

  test('OSK cluster does not have Metrics tab', async ({ page }) => {
    await page.click('text=staging-kafka-cluster')

    // Verify no Metrics tab
    await expect(page.locator('nav button:has-text("Metrics")')).not.toBeVisible()
  })

  test('MSK cluster has Metrics tab', async ({ page }) => {
    await page.click('text=msk-cluster-1')

    // Verify Metrics tab exists
    await expect(page.locator('nav button:has-text("Metrics")')).toBeVisible()
  })
})
