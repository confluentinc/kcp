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

  test('uploading a valid state file from a different KCP version succeeds', async ({ page }) => {
    await page.goto('/')

    const mismatchedState = {
      kcp_build_info: { version: '0.1.0', commit: 'abc', date: '2024-01-01' },
      msk_sources: { regions: [] },
      osk_sources: { clusters: [] },
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
  })
})
