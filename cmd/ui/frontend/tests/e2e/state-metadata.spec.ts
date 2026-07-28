import { test, expect } from '@playwright/test'
import stateMetadata from './fixtures/state-metadata.json' with { type: 'json' }

test.describe('State file metadata popover', () => {
  test('shows schema version, build, timestamps and migration provenance', async ({ page }) => {
    await page.goto('/')

    // Upload the metadata-rich fixture (replaces the pre-loaded state).
    await page.click('button:has-text("Upload KCP State File")')
    await page.locator('input[type="file"]').setInputFiles({
      name: 'state-metadata.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateMetadata)),
    })

    // The metadata trigger appears once state is loaded; open it.
    const trigger = page.getByTestId('state-metadata-trigger')
    await expect(trigger).toBeVisible({ timeout: 10000 })
    await trigger.click()

    await expect(page.getByTestId('meta-schema')).toHaveText('1')
    await expect(page.getByTestId('meta-build')).toHaveText('0.8.5')
    await expect(page.getByTestId('meta-upgraded')).toHaveText('kcp_build_info.version=0.7.3')
    await expect(page.getByTestId('meta-created')).toBeVisible()
    await expect(page.getByTestId('meta-updated')).toBeVisible()
  })
})
