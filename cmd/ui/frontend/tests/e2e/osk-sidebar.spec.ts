import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json' with { type: 'json' }

test.describe('OSK Sidebar', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

    // Upload OSK-only state file via file input
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-osk-only.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateOSKOnly)),
    })

    // Wait for state to be processed
    await page.waitForTimeout(500)
  })

  test('displays OSK section when OSK clusters present', async ({ page }) => {
    // Verify "OPEN SOURCE KAFKA" section appears
    await expect(page.locator('text=OPEN SOURCE KAFKA')).toBeVisible()

    // Verify OSK cluster is listed
    await expect(page.locator('text=prod-kafka-cluster')).toBeVisible()
  })

  test('does not display MSK section when no MSK clusters', async ({ page }) => {
    // Verify "AWS MSK" section is not present
    await expect(page.locator('text=AWS MSK')).not.toBeVisible()
  })

  test('selects OSK cluster on click', async ({ page }) => {
    // Click on OSK cluster
    await page.click('text=prod-kafka-cluster')

    // Verify cluster detail view appears
    await expect(page.locator('h1:has-text("Cluster: prod-kafka-cluster")')).toBeVisible()

    // Verify cluster is highlighted in sidebar
    const clusterButton = page.locator('button:has-text("prod-kafka-cluster")')
    await expect(clusterButton).toHaveClass(/bg-blue-100/)
  })
})
