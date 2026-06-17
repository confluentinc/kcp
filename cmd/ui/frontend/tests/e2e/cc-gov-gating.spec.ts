import { test, expect } from '@playwright/test'

// Gating of linking-based wizards for Confluent Cloud for Government.
// Each affected wizard asks the Standard/Gov destination question first; Gov
// routes to a terminal blocked step (no path to generation) on the unsupported
// paths, while Standard proceeds to the wizard's existing flow.

const BLOCKED_TITLE = 'Unsupported on Confluent Cloud for Government'

test.describe('CC for Government gating — migration-infra wizards', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForSelector('nav button', { timeout: 10000 })
    await page.locator('nav button:has-text("Migrate")').click()
  })

  test('MSK infra wizard — Gov is blocked before generation (AE6)', async ({ page }) => {
    await page.waitForSelector('text=Managed Streaming for Kafka', { timeout: 10000 })
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Select Gov (index 1)
    await page.locator('#root_cc_environment-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Terminal blocked step: names the product, explains Cluster Linking, and
    // offers no route to generation.
    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('Cluster Linking')).toBeVisible()
    await expect(page.locator('button[type="submit"]')).toHaveCount(0)
    await expect(page.locator('button:has-text("Generate Terraform Files")')).toHaveCount(0)
  })

  test('MSK infra wizard — Standard proceeds to existing flow', async ({ page }) => {
    await page.waitForSelector('text=Managed Streaming for Kafka', { timeout: 10000 })
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Select Standard (index 0)
    await page.locator('#root_cc_environment-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // The wizard's existing first question is now reachable.
    await expect(page.locator('#root_has_public_brokers-0')).toBeVisible({ timeout: 5000 })
  })

  test('MSK infra wizard — Back from blocked step returns to the destination question', async ({
    page,
  }) => {
    await page.waitForSelector('text=Managed Streaming for Kafka', { timeout: 10000 })
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    await page.locator('#root_cc_environment-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toBeVisible({ timeout: 5000 })

    // Back must not be a dead-end — it returns to the destination question.
    await page.getByRole('button', { name: 'Back', exact: true }).click()
    await page.waitForTimeout(500)
    await expect(page.locator('#root_cc_environment-0')).toBeVisible({ timeout: 5000 })
  })

  test('OSK infra wizard — Gov is blocked, Standard proceeds', async ({ page }) => {
    await page.waitForSelector('text=Apache Kafka', { timeout: 10000 })
    await page.locator('text=production-kafka-us-east').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Gov → blocked
    await page.locator('#root_cc_environment-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)
    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toBeVisible({ timeout: 5000 })
    await expect(page.locator('button[type="submit"]')).toHaveCount(0)

    // Back to destination, then Standard → existing flow
    await page.getByRole('button', { name: 'Back', exact: true }).click()
    await page.waitForTimeout(500)
    await page.locator('#root_cc_environment-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)
    await expect(page.locator('#root_has_public_brokers-0')).toBeVisible({ timeout: 5000 })
  })
})

test.describe('CC for Government gating — topic migration scripts wizard', () => {
  // Opens the topic-scripts wizard for the MSK playground cluster, landing on
  // the destination question.
  const openTopicsWizard = async (page: import('@playwright/test').Page) => {
    await page.goto('/')
    await page.waitForSelector('nav button', { timeout: 10000 })
    await page.locator('nav button:has-text("Migrate")').click()
    await page.waitForSelector('text=Managed Streaming for Kafka', { timeout: 10000 })
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)
    await page.locator('button:has-text("Generate Assets")').click()
    await page.waitForTimeout(500)
    await page.locator('button:has-text("Topic Migration Scripts")').click()
    await page.waitForTimeout(500)
  }

  test('Gov + mirror is blocked (AE3 / AE6)', async ({ page }) => {
    await openTopicsWizard(page)

    // Gov (index 1)
    await page.locator('#root_cc_environment-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Mirror is the default mode — submit straight through.
    await page.locator('#root_mode-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('Cluster Linking')).toBeVisible()
    await expect(page.locator('button[type="submit"]')).toHaveCount(0)
  })

  test('Gov + new proceeds to new-topic inputs (AE3)', async ({ page }) => {
    await openTopicsWizard(page)

    await page.locator('#root_cc_environment-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select "new" mode (index 1) — allowed under Gov.
    await page.locator('#root_mode-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // New-topic target inputs are reachable; no cluster-link field, no block.
    await expect(page.locator('#root_target_cluster_id')).toBeVisible({ timeout: 5000 })
    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toHaveCount(0)
    await expect(page.locator('#root_cluster_link_name')).toHaveCount(0)
  })

  test('Standard + mirror proceeds to mirror inputs', async ({ page }) => {
    await openTopicsWizard(page)

    // Standard (index 0)
    await page.locator('#root_cc_environment-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Mirror mode (default).
    await page.locator('#root_mode-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Mirror inputs include the existing cluster-link field.
    await expect(page.locator('#root_cluster_link_name')).toBeVisible({ timeout: 5000 })
    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toHaveCount(0)
  })

  test('Standard + new proceeds to new-topic inputs', async ({ page }) => {
    await openTopicsWizard(page)

    // Standard (index 0)
    await page.locator('#root_cc_environment-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // New mode (index 1).
    await page.locator('#root_mode-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // New-topic inputs reachable; no cluster-link field, no block.
    await expect(page.locator('#root_target_cluster_id')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('#root_cluster_link_name')).toHaveCount(0)
    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toHaveCount(0)
  })

  test('Switching Gov→mirror then Back→new routes correctly (guard reads current allData)', async ({
    page,
  }) => {
    await openTopicsWizard(page)

    await page.locator('#root_cc_environment-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Gov + mirror → blocked
    await page.locator('#root_mode-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)
    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toBeVisible({ timeout: 5000 })

    // Back to mode selection, switch to new → proceeds (still Gov).
    // Exact match avoids the wrapping "Back to Selection" button.
    await page.getByRole('button', { name: 'Back', exact: true }).click()
    await page.waitForTimeout(500)
    await page.locator('#root_mode-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)
    await expect(page.locator('#root_target_cluster_id')).toBeVisible({ timeout: 5000 })
  })
})

test.describe('CC for Government gating — schema registry (exporter) wizard', () => {
  const openSchemaRegistryWizard = async (page: import('@playwright/test').Page) => {
    await page.goto('/')
    await page.waitForSelector('nav button', { timeout: 10000 })
    await page.locator('nav button:has-text("Migrate")').click()
    await page.waitForSelector('text=Managed Streaming for Kafka', { timeout: 10000 })
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)
    await page.locator('button:has-text("Generate Assets")').click()
    await page.waitForTimeout(500)
    await page.locator('button:has-text("Schema Registry Migration Scripts")').click()
    await page.waitForTimeout(500)
  }

  test('Exporter wizard — Gov is blocked (AE6)', async ({ page }) => {
    await openSchemaRegistryWizard(page)

    await page.locator('#root_cc_environment-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    await expect(page.locator(`h2:has-text("${BLOCKED_TITLE}")`)).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('Schema Linking')).toBeVisible()
    await expect(page.locator('button[type="submit"]')).toHaveCount(0)
  })

  test('Exporter wizard — Standard proceeds to the CC SR URL step', async ({ page }) => {
    await openSchemaRegistryWizard(page)

    await page.locator('#root_cc_environment-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    await expect(page.locator('#root_confluent_cloud_schema_registry_url')).toBeVisible({
      timeout: 5000,
    })
  })

  // AE7: untouched wizards never ask the destination question. target-infra is
  // always available; the glue wizard is verified by code review (not modified)
  // and is absent from this fixture's state.
  test('target-infra wizard has no destination question (AE7)', async ({ page }) => {
    await page.goto('/')
    await page.waitForSelector('nav button', { timeout: 10000 })
    await page.locator('nav button:has-text("Migrate")').click()
    await page.waitForSelector('text=Managed Streaming for Kafka', { timeout: 10000 })
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)

    // First phase "Generate Terraform" (nth 0) is target-infra.
    await page.locator('button:has-text("Generate Terraform")').nth(0).click()
    await page.waitForTimeout(500)

    // No destination radio; the wizard's own first step is shown.
    await expect(page.locator('#root_cc_environment-0')).toHaveCount(0)
    await expect(page.locator('button[type="submit"]')).toBeVisible({ timeout: 5000 })
  })
})
