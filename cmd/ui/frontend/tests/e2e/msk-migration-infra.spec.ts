import { test, expect } from '@playwright/test'

test.describe('MSK Migration Infrastructure Wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    // Wait for pre-loaded state to be processed - tabs only render once kcpState is loaded
    await page.waitForSelector('nav button', { timeout: 10000 })
    // Navigate to Migrate tab (scoped to nav to avoid matching other buttons)
    await page.locator('nav button:has-text("Migrate")').click()
    // Wait for the Migration Assets page to render with MSK section
    await page.waitForSelector('h2:has-text("Managed Streaming for Kafka (MSK)")', { timeout: 10000 })
  })

  test('Public path - pre-populated fields are disabled', async ({ page }) => {
    // MSK clusters should be visible under section heading
    await expect(page.locator('h2:has-text("Managed Streaming for Kafka (MSK)")')).toBeVisible()

    // The first MSK cluster is auto-expanded by default - wait for phase cards to be visible
    await expect(page.locator('h4:has-text("Migration Infrastructure")')).toBeVisible({ timeout: 5000 })

    // Click Generate Terraform for Migration Infrastructure (phase 2)
    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()

    // Wizard opens in modal - should see MSK-specific title
    await expect(page.locator('h2:has-text("MSK Migration - Public or Private Networking")')).toBeVisible({ timeout: 5000 })

    // Select public (Yes)
    await page.locator('label:has-text("Yes")').click()
    await page.locator('button[type="submit"]').click()

    // Public cluster link configuration step
    await expect(page.locator('h2:has-text("Public Migration | Cluster Link Configuration")')).toBeVisible({ timeout: 5000 })

    // Source cluster ID and bootstrap servers should be pre-populated and disabled
    const clusterIdInput = page.locator('#root_source_cluster_id')
    await expect(clusterIdInput).toBeDisabled()
    await expect(clusterIdInput).not.toBeEmpty()

    const bootstrapInput = page.locator('#root_source_sasl_scram_bootstrap_servers')
    await expect(bootstrapInput).toBeDisabled()
    await expect(bootstrapInput).not.toBeEmpty()

    // Fill editable target fields
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'msk-to-cc-link')

    await page.locator('button[type="submit"]').click()

    // Confirmation
    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('External Outbound path', async ({ page }) => {
    // The first MSK cluster is auto-expanded by default - wait for phase cards
    await expect(page.locator('h4:has-text("Migration Infrastructure")')).toBeVisible({ timeout: 5000 })

    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()

    // Wizard opens - select private (No)
    await expect(page.locator('h2:has-text("MSK Migration - Public or Private Networking")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()

    // Private migration method question
    await expect(page.locator('h2:has-text("Private Migration | Method")')).toBeVisible({ timeout: 5000 })

    // Select external outbound
    await page.locator('label:has-text("No, use external outbound cluster linking")').click()
    await page.locator('button[type="submit"]').click()

    // External outbound cluster linking inputs
    await expect(page.locator('h2:has-text("Private Migration | External Outbound Cluster Linking")')).toBeVisible({ timeout: 5000 })

    // Fill visible editable fields -- many MSK fields are pre-populated and hidden
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'msk-ext-outbound-link')
    await page.fill('#root_target_environment_id', 'env-msk-test')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-msk-test.confluent.cloud:9092')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')

    await page.locator('button[type="submit"]').click()

    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster SASL/SCRAM path - auth question appears with both options', async ({ page }) => {
    // The first MSK cluster is auto-expanded by default - wait for phase cards
    await expect(page.locator('h4:has-text("Migration Infrastructure")')).toBeVisible({ timeout: 5000 })

    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()

    // Private -> Jump Cluster
    await expect(page.locator('h2:has-text("MSK Migration - Public or Private Networking")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()

    // Select jump cluster (Yes)
    await expect(page.locator('h2:has-text("Private Migration | Method")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("Yes")').first().click()
    await page.locator('button[type="submit"]').click()

    // Internet gateway question
    await expect(page.locator('h2:has-text("Private Migration | Private Link - Internet Gateway")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()

    // Jump cluster networking inputs - some fields pre-populated from MSK state
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Configuration")')).toBeVisible({ timeout: 5000 })

    // VPC ID should be pre-populated and disabled for MSK
    const vpcInput = page.locator('#root_vpc_id')
    await expect(vpcInput).toBeDisabled()

    // Fill editable fields
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.locator('button[type="submit"]').click()

    // Auth question should appear with BOTH options for MSK
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication")')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('label:has-text("SASL/SCRAM")')).toBeVisible()
    await expect(page.locator('label:has-text("IAM")')).toBeVisible()

    // Select SASL/SCRAM
    await page.locator('label:has-text("SASL/SCRAM")').click()
    await page.locator('button[type="submit"]').click()

    // SASL/SCRAM auth step - fill target cluster details
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication (SASL/SCRAM)")')).toBeVisible({ timeout: 5000 })

    await page.fill('#root_target_environment_id', 'env-msk-test')
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-msk-test.confluent.cloud:9092')
    await page.fill('#root_cluster_link_name', 'msk-jump-sasl-link')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test456')

    await page.locator('button[type="submit"]').click()

    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster IAM path - IAM role field appears', async ({ page }) => {
    // The first MSK cluster is auto-expanded by default - wait for phase cards
    await expect(page.locator('h4:has-text("Migration Infrastructure")')).toBeVisible({ timeout: 5000 })

    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()

    // Private -> Jump Cluster
    await expect(page.locator('h2:has-text("MSK Migration - Public or Private Networking")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()

    await expect(page.locator('h2:has-text("Private Migration | Method")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("Yes")').first().click()
    await page.locator('button[type="submit"]').click()

    // Internet gateway
    await expect(page.locator('h2:has-text("Private Migration | Private Link - Internet Gateway")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()

    // Jump cluster networking - fill required editable fields
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Configuration")')).toBeVisible({ timeout: 5000 })
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.locator('button[type="submit"]').click()

    // Select IAM auth
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication")')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('label:has-text("IAM")')).toBeVisible()
    await page.locator('label:has-text("IAM")').click()
    await page.locator('button[type="submit"]').click()

    // IAM auth step - should show IAM-specific fields
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication (IAM)")')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('label:has-text("Instance Role Name")')).toBeVisible()

    // Fill IAM-specific fields
    await page.fill('#root_target_environment_id', 'env-msk-test')
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-msk-test.confluent.cloud:9092')
    await page.fill('#root_cluster_link_name', 'msk-jump-iam-link')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test456')
    await page.fill('#root_jump_cluster_iam_auth_role_name', 'msk-cluster-link-role')

    await page.locator('button[type="submit"]').click()

    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })
})
