import { test, expect } from '@playwright/test'
import stateBoth from '../fixtures/state-both.json' with { type: 'json' }

test.describe('Switching Between MSK and OSK', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

    // Upload state with both MSK and OSK
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-both.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateBoth)),
    })

    await page.waitForTimeout(500)
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
    await page.click('text=Summary')

    // Verify cost analysis appears (MSK-only feature)
    await expect(page.locator('text=Cost Analysis Summary')).toBeVisible()
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
