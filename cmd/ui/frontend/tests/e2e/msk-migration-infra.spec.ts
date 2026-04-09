import { test, expect } from '@playwright/test'

test.describe('MSK Migration Infrastructure Wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForSelector('nav button', { timeout: 10000 })
    await page.locator('nav button:has-text("Migrate")').click()
    await page.waitForSelector('text=Managed Streaming for Kafka', { timeout: 10000 })
  })

  test('Public path - pre-populated fields are disabled', async ({ page }) => {
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Select "Yes" for public brokers (index 0)
    await page.locator('#root_has_public_brokers-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Source fields should be pre-populated and disabled
    const clusterIdInput = page.locator('#root_source_cluster_id')
    await expect(clusterIdInput).toBeVisible()
    await expect(clusterIdInput).toBeDisabled()

    const bootstrapInput = page.locator('#root_source_sasl_scram_bootstrap_servers')
    await expect(bootstrapInput).toBeVisible()
    await expect(bootstrapInput).toBeDisabled()

    // Fill editable target fields
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'msk-to-cc-link')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Confirmation
    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })

  test('External Outbound path', async ({ page }) => {
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Select private (index 1 = No)
    await page.locator('#root_has_public_brokers-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select Enterprise target cluster type
    await page.locator('#root_target_cluster_type-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select External Outbound SASL/SCRAM (index 0)
    await page.locator('#root_private_migration_method-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Fill fields
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'msk-ext-outbound-link')
    await page.fill('#root_target_environment_id', 'env-msk-test')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster SASL/SCRAM path - auth question appears with both options', async ({ page }) => {
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Private (index 1 = No)
    await page.locator('#root_has_public_brokers-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select Enterprise target cluster type
    await page.locator('#root_target_cluster_type-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Jump Cluster (index 2)
    await page.locator('#root_private_migration_method-2').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Internet gateway - No (index 1)
    await page.locator('#root_has_existing_internet_gateway-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Networking inputs - fill required fields not pre-populated
    // vpc_id, instance_type, storage are pre-populated from MSK state
    // but existing_private_link_vpce_id, broker CIDRs, setup host CIDR need filling
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_broker_subnet_cidr_0', '10.0.1.0/24')
    await page.fill('#root_jump_cluster_broker_subnet_cidr_1', '10.0.2.0/24')
    await page.fill('#root_jump_cluster_broker_subnet_cidr_2', '10.0.3.0/24')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Auth question should appear with BOTH options
    await expect(page.locator('label:has-text("SASL/SCRAM")')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('label:has-text("IAM")')).toBeVisible()

    // Select SASL/SCRAM (index 0)
    await page.locator('#root_jump_cluster_auth_type-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Fill target cluster details
    await page.fill('#root_target_environment_id', 'env-msk-test')
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-msk-test.confluent.cloud:9092')
    await page.fill('#root_cluster_link_name', 'msk-jump-sasl-link')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test456')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster IAM path - IAM role field appears', async ({ page }) => {
    await page.locator('text=kcp-playground').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Private (index 1 = No)
    await page.locator('#root_has_public_brokers-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select Enterprise target cluster type
    await page.locator('#root_target_cluster_type-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Jump Cluster (index 2)
    await page.locator('#root_private_migration_method-2').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Internet gateway - No (index 1)
    await page.locator('#root_has_existing_internet_gateway-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Networking - fill required fields
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_broker_subnet_cidr_0', '10.0.1.0/24')
    await page.fill('#root_jump_cluster_broker_subnet_cidr_1', '10.0.2.0/24')
    await page.fill('#root_jump_cluster_broker_subnet_cidr_2', '10.0.3.0/24')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select IAM auth (index 1)
    await page.locator('#root_jump_cluster_auth_type-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // IAM-specific field should appear
    await expect(page.locator('label:has-text("Instance Role Name")')).toBeVisible({ timeout: 5000 })

    // Fill fields
    await page.fill('#root_target_environment_id', 'env-msk-test')
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-msk-test.confluent.cloud:9092')
    await page.fill('#root_cluster_link_name', 'msk-jump-iam-link')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test456')
    await page.fill('#root_jump_cluster_iam_auth_role_name', 'msk-cluster-link-role')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })
})
