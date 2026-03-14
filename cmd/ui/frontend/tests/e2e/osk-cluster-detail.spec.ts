import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json' with { type: 'json' }

test.describe('OSK Cluster Detail View', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

    // Click the upload button to reveal the file input
    await page.click('button:has-text("Upload KCP State File")')

    // Upload state file
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-osk-only.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateOSKOnly)),
    })

    await page.waitForTimeout(500)

    // Navigate to OSK cluster
    await page.click('text=prod-kafka-cluster')
  })

  test('displays correct tabs for OSK cluster', async ({ page }) => {
    // Verify OSK-specific tabs are present
    await expect(page.locator('nav button:has-text("Cluster")')).toBeVisible()
    await expect(page.locator('nav button:has-text("Topics")')).toBeVisible()
    await expect(page.locator('nav button:has-text("ACLs")')).toBeVisible()
    await expect(page.locator('nav button:has-text("Connectors")')).toBeVisible()

    // Verify Metrics tab is NOT present
    await expect(page.locator('nav button:has-text("Metrics")')).not.toBeVisible()
  })

  test('displays bootstrap servers in cluster tab', async ({ page }) => {
    // Cluster tab should be selected by default
    await expect(page.locator('text=Bootstrap Servers')).toBeVisible()
    await expect(page.locator('text=broker1.example.com:9092')).toBeVisible()
    await expect(page.locator('text=broker2.example.com:9092')).toBeVisible()
  })

  test('displays metadata fields', async ({ page }) => {
    // Verify metadata section
    await expect(page.locator('text=Cluster Metadata')).toBeVisible()
    await expect(page.locator('div.text-sm:has-text("Environment")').first()).toBeVisible()
    await expect(page.locator('span.font-medium:has-text("production")')).toBeVisible()
    await expect(page.locator('div.text-sm:has-text("Location")').first()).toBeVisible()
    await expect(page.locator('span.font-medium:has-text("datacenter-1")')).toBeVisible()
    await expect(page.locator('div.text-sm:has-text("Kafka Version")').first()).toBeVisible()
    await expect(page.locator('span.font-medium:has-text("3.6.0")')).toBeVisible()
  })

  test('displays labels', async ({ page }) => {
    await expect(page.locator('text=Labels')).toBeVisible()
    await expect(page.locator('text=team: platform')).toBeVisible()
    await expect(page.locator('text=cost-center: engineering')).toBeVisible()
  })

  test('Topics tab shows Kafka topics', async ({ page }) => {
    await page.click('nav button:has-text("Topics")')

    // Verify topics appear (reusing MSK component)
    await expect(page.locator('text=orders')).toBeVisible()
  })

  test('ACLs tab shows Kafka ACLs', async ({ page }) => {
    await page.click('nav button:has-text("ACLs")')

    // Verify ACLs appear
    await expect(page.locator('text=User:alice')).toBeVisible()
  })
})
