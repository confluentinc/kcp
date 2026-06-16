import { test, expect } from '@playwright/test'
import stateApacheKafkaOnly from '../fixtures/state-apache-kafka-only.json' with { type: 'json' }

test.describe('Apache Kafka Sidebar', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

    // Click the upload button to reveal the file input
    await page.click('button:has-text("Upload KCP State File")')

    // Upload Apache Kafka-only state file via file input
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-apache-kafka-only.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateApacheKafkaOnly)),
    })

    // Wait for state to be processed
    await page.waitForTimeout(500)
  })

  test('displays Apache Kafka section when Apache Kafka clusters present', async ({ page }) => {
    // Verify "Apache Kafka" section appears
    await expect(page.locator('text=Apache Kafka')).toBeVisible()

    // Verify Apache Kafka cluster is listed
    await expect(page.locator('button:has-text("prod-kafka-cluster")')).toBeVisible()
  })

  test('does not display MSK section when no MSK clusters', async ({ page }) => {
    // Verify "AWS MSK" section is not present
    await expect(page.locator('text=AWS MSK')).not.toBeVisible()
  })

  test('selects Apache Kafka cluster on click', async ({ page }) => {
    // Click on Apache Kafka cluster
    await page.click('text=prod-kafka-cluster')

    // Verify cluster detail view appears
    await expect(page.locator('h1:has-text("Cluster: prod-kafka-cluster")')).toBeVisible()

    // Verify cluster is highlighted in sidebar
    const clusterButton = page.locator('button:has-text("prod-kafka-cluster")')
    await expect(clusterButton).toHaveClass(/bg-accent\/10/)
  })
})
