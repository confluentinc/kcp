import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json' with { type: 'json' }

test.describe('OSK Cluster Detail View', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

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
    await expect(page.locator('button[role="tab"]:has-text("Cluster")')).toBeVisible()
    await expect(page.locator('button[role="tab"]:has-text("Topics")')).toBeVisible()
    await expect(page.locator('button[role="tab"]:has-text("ACLs")')).toBeVisible()
    await expect(page.locator('button[role="tab"]:has-text("Connectors")')).toBeVisible()

    // Verify Metrics tab is NOT present
    await expect(page.locator('button[role="tab"]:has-text("Metrics")')).not.toBeVisible()
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
    await expect(page.locator('text=Environment')).toBeVisible()
    await expect(page.locator('text=production')).toBeVisible()
    await expect(page.locator('text=Location')).toBeVisible()
    await expect(page.locator('text=datacenter-1')).toBeVisible()
    await expect(page.locator('text=Kafka Version')).toBeVisible()
    await expect(page.locator('text=3.6.0')).toBeVisible()
  })

  test('displays labels', async ({ page }) => {
    await expect(page.locator('text=Labels')).toBeVisible()
    await expect(page.locator('text=team: platform')).toBeVisible()
    await expect(page.locator('text=cost-center: engineering')).toBeVisible()
  })

  test('Topics tab shows Kafka topics', async ({ page }) => {
    await page.click('button[role="tab"]:has-text("Topics")')

    // Verify topics appear (reusing MSK component)
    await expect(page.locator('text=orders')).toBeVisible()
  })

  test('ACLs tab shows Kafka ACLs', async ({ page }) => {
    await page.click('button[role="tab"]:has-text("ACLs")')

    // Verify ACLs appear
    await expect(page.locator('text=User:alice')).toBeVisible()
  })
})
