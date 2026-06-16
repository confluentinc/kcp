import { test, expect } from '@playwright/test'

test.describe('State File Validation', () => {
  test('uploading a JSON array shows an invalid file format error', async ({ page }) => {
    await page.goto('/')

    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'not-a-state-file.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify([{ foo: 'bar' }])),
    })

    await expect(page.locator('text=Invalid file format')).toBeVisible({ timeout: 5000 })
  })

  test('uploading a valid state file from a different KCP version with sources succeeds', async ({ page }) => {
    await page.goto('/')

    const mismatchedState = {
      kcp_build_info: { version: '0.1.0', commit: 'abc', date: '2024-01-01' },
      msk_sources: { regions: [] },
      apache_kafka_sources: {
        clusters: [
          {
            id: 'test-cluster',
            bootstrap_servers: ['localhost:9092'],
            kafka_admin_client_information: { cluster_id: 'test', discovered_brokers: ['localhost:9092'] },
            discovered_clients: null,
            metadata: { last_scanned: '2024-01-01T00:00:00Z' },
          },
        ],
      },
    }

    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'old-state.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(mismatchedState)),
    })

    // Should load without error — version mismatch alone is not a rejection
    await expect(page.locator('text=Invalid file format')).not.toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=no sources')).not.toBeVisible({ timeout: 5000 })
  })

  test('uploading a state file with no sources and no schema registries shows error', async ({ page }) => {
    await page.goto('/')

    const emptyState = {
      kcp_build_info: { version: '0.1.0', commit: 'abc', date: '2024-01-01' },
      msk_sources: { regions: [] },
      apache_kafka_sources: { clusters: [] },
    }

    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'empty-state.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(emptyState)),
    })

    await expect(page.locator('text=no sources or schema registries')).toBeVisible({ timeout: 5000 })
  })

  test('uploading an empty JSON object shows error', async ({ page }) => {
    await page.goto('/')

    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'empty.json',
      mimeType: 'application/json',
      buffer: Buffer.from('{}'),
    })

    await expect(page.locator('text=no sources or schema registries')).toBeVisible({ timeout: 5000 })
  })

  test('uploading a state file with schema registries but no sources shows warning', async ({ page }) => {
    await page.goto('/')

    const schemaOnlyState = {
      kcp_build_info: { version: '0.1.0', commit: 'abc', date: '2024-01-01' },
      msk_sources: { regions: [] },
      apache_kafka_sources: { clusters: [] },
      schema_registries: {
        confluent_schema_registry: [
          { type: 'confluent', url: 'http://localhost:8081', subjects: [] },
        ],
      },
    }

    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'schema-only.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(schemaOnlyState)),
    })

    await expect(page.locator('text=No cluster sources found')).toBeVisible({ timeout: 5000 })
  })

  test('uploading invalid JSON shows error', async ({ page }) => {
    await page.goto('/')

    await page.click('button:has-text("Upload KCP State File")')
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'broken.json',
      mimeType: 'application/json',
      buffer: Buffer.from('not valid json'),
    })

    await expect(page.locator('text=invalid JSON')).toBeVisible({ timeout: 5000 })
  })
})
