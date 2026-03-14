import { test, expect } from '@playwright/test'

test.describe('Switching Between MSK and OSK', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    // State is pre-loaded via --state-file flag (state-migration.json with both MSK and OSK)
    // Wait for both sections to appear in sidebar
    await page.waitForSelector('text=AWS MSK', { timeout: 10000 })
    await page.waitForSelector('text=OPEN SOURCE KAFKA', { timeout: 5000 })
  })

  test('displays both MSK and OSK sections', async ({ page }) => {
    await expect(page.locator('text=AWS MSK')).toBeVisible()
    await expect(page.locator('text=OPEN SOURCE KAFKA')).toBeVisible()
  })

  test('can switch from MSK cluster to OSK cluster', async ({ page }) => {
    // Click MSK cluster
    await page.click('text=kcp-playground')
    await expect(page.locator('h1:has-text("kcp-playground")')).toBeVisible()

    // Click OSK cluster
    await page.click('text=production-kafka-us-east')
    await expect(page.locator('text=Bootstrap Servers')).toBeVisible()
  })

  test('MSK summary only shows MSK data', async ({ page }) => {
    // Click Summary button
    await page.click('button:has-text("Summary")')

    // Wait for the Summary view to render
    await page.waitForSelector('h1:has-text("MSK Cost Summary")', { timeout: 5000 })

    await expect(page.locator('text=MSK Cost Summary')).toBeVisible()
  })

  test('OSK cluster does not have Metrics tab', async ({ page }) => {
    await page.click('text=production-kafka-us-east')

    // Verify no Metrics tab for OSK
    await expect(page.locator('nav button:has-text("Metrics")')).not.toBeVisible()
  })

  test('MSK cluster has Metrics tab', async ({ page }) => {
    await page.click('text=kcp-playground')

    // Verify Metrics tab exists for MSK
    await expect(page.locator('nav button:has-text("Metrics")')).toBeVisible()
  })
})
