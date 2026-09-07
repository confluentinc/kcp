import { test, expect } from '@playwright/test'
import stateFixture from './fixtures/state-topic-cleanup-policy.json' with { type: 'json' }

test.describe('Topic cleanup-policy badges', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.click('button:has-text("Upload KCP State File")')
    await page.locator('input[type="file"]').setInputFiles({
      name: 'state-topic-cleanup-policy.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateFixture)),
    })
    await page.waitForTimeout(500)
    await page.click('text=cleanup-policy-test-cluster')
    await page.click('nav button:has-text("Topics")')
  })

  test('shows only the Delete badge for a delete-only topic', async ({ page }) => {
    const row = page.locator('tbody tr').filter({ hasText: 'orders-events' })
    await expect(row.getByText('Delete', { exact: true })).toBeVisible()
    await expect(row.getByText('Compact', { exact: true })).toHaveCount(0)
  })

  test('shows only the Compact badge for a compact-only topic', async ({ page }) => {
    const row = page.locator('tbody tr').filter({ hasText: 'ktable-changelog' })
    await expect(row.getByText('Compact', { exact: true })).toBeVisible()
    await expect(row.getByText('Delete', { exact: true })).toHaveCount(0)
  })

  test('shows both badges for a topic with cleanup.policy=compact,delete', async ({ page }) => {
    const row = page.locator('tbody tr').filter({ hasText: 'audit-log' })
    await expect(row.getByText('Compact', { exact: true })).toBeVisible()
    await expect(row.getByText('Delete', { exact: true })).toBeVisible()
  })

  test('shows both badges regardless of policy order (delete,compact)', async ({ page }) => {
    const row = page.locator('tbody tr').filter({ hasText: 'reversed-policy-topic' })
    await expect(row.getByText('Compact', { exact: true })).toBeVisible()
    await expect(row.getByText('Delete', { exact: true })).toBeVisible()
  })

  test('internal compacted topic still shows the Internal tag alongside Compact', async ({ page }) => {
    const row = page.locator('tbody tr').filter({ hasText: '__consumer_offsets' })
    await expect(row.getByText('Internal', { exact: true })).toBeVisible()
    await expect(row.getByText('Compact', { exact: true })).toBeVisible()
    await expect(row.getByText('Delete', { exact: true })).toHaveCount(0)
  })
})
