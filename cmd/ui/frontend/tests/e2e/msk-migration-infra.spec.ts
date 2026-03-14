import { test, expect } from '@playwright/test'

test.describe('MSK Migration Infrastructure Wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    // Wait for pre-loaded state to be processed
    await page.waitForTimeout(2000)
    // Navigate to Migrate tab
    await page.click('button:has-text("Migrate")')
    await page.waitForTimeout(500)
  })

  test('Public path - pre-populated fields are disabled', async ({ page }) => {
    // MSK clusters should be visible under section heading
    await expect(page.locator('h2:has-text("Managed Streaming for Kafka (MSK)")')).toBeVisible()

    // Click on the MSK cluster accordion to expand it
    await page.click('h3:has-text("kcp-playground")')
    await page.waitForTimeout(300)

    // Click Generate Terraform for Migration Infrastructure (phase 2)
    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()
    await page.waitForTimeout(500)

    // Should see MSK-specific title
    await expect(page.locator('h2:has-text("MSK Migration - Public or Private Networking")')).toBeVisible()

    // Select public (Yes)
    await page.locator('label:has-text("Yes")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Public cluster link configuration step
    await expect(page.locator('h2:has-text("Public Migration | Cluster Link Configuration")')).toBeVisible()

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

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Confirmation
    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible()
    await page.click('button:has-text("Generate Terraform Files")')
    await page.waitForTimeout(2000)

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('External Outbound path', async ({ page }) => {
    await page.click('h3:has-text("kcp-playground")')
    await page.waitForTimeout(300)

    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()
    await page.waitForTimeout(500)

    // Select private (No)
    await page.locator('label:has-text("No")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Private migration method question
    await expect(page.locator('h2:has-text("Private Migration | Method")')).toBeVisible()

    // Select external outbound
    await page.locator('label:has-text("No, use external outbound cluster linking")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // External outbound cluster linking inputs
    await expect(page.locator('h2:has-text("Private Migration | External Outbound Cluster Linking")')).toBeVisible()

    // Fill visible editable fields -- many MSK fields are pre-populated and hidden
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'msk-ext-outbound-link')
    await page.fill('#root_target_environment_id', 'env-msk-test')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-msk-test.confluent.cloud:9092')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible()
    await page.click('button:has-text("Generate Terraform Files")')
    await page.waitForTimeout(2000)

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster SASL/SCRAM path - auth question appears with both options', async ({ page }) => {
    await page.click('h3:has-text("kcp-playground")')
    await page.waitForTimeout(300)

    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()
    await page.waitForTimeout(500)

    // Private -> Jump Cluster
    await page.locator('label:has-text("No")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Select jump cluster (Yes)
    await page.locator('label:has-text("Yes")').first().click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Internet gateway question
    await expect(page.locator('h2:has-text("Private Migration | Private Link - Internet Gateway")')).toBeVisible()
    await page.locator('label:has-text("No")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Jump cluster networking inputs - some fields pre-populated from MSK state
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Configuration")')).toBeVisible()

    // VPC ID should be pre-populated and disabled for MSK
    const vpcInput = page.locator('#root_vpc_id')
    await expect(vpcInput).toBeDisabled()

    // Fill editable fields
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Auth question should appear with BOTH options for MSK
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication")')).toBeVisible()
    await expect(page.locator('label:has-text("SASL/SCRAM")')).toBeVisible()
    await expect(page.locator('label:has-text("IAM")')).toBeVisible()

    // Select SASL/SCRAM
    await page.locator('label:has-text("SASL/SCRAM")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // SASL/SCRAM auth step - fill target cluster details
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication (SASL/SCRAM)")')).toBeVisible()

    await page.fill('#root_target_environment_id', 'env-msk-test')
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-msk-test.confluent.cloud:9092')
    await page.fill('#root_cluster_link_name', 'msk-jump-sasl-link')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test456')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible()
    await page.click('button:has-text("Generate Terraform Files")')
    await page.waitForTimeout(2000)

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster IAM path - IAM role field appears', async ({ page }) => {
    await page.click('h3:has-text("kcp-playground")')
    await page.waitForTimeout(300)

    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()
    await page.waitForTimeout(500)

    // Private -> Jump Cluster
    await page.locator('label:has-text("No")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    await page.locator('label:has-text("Yes")').first().click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Internet gateway
    await page.locator('label:has-text("No")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Jump cluster networking - fill required editable fields
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Select IAM auth
    await expect(page.locator('label:has-text("IAM")')).toBeVisible()
    await page.locator('label:has-text("IAM")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // IAM auth step - should show IAM-specific fields
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication (IAM)")')).toBeVisible()
    await expect(page.locator('label:has-text("Instance Role Name")')).toBeVisible()

    // Fill IAM-specific fields
    await page.fill('#root_target_environment_id', 'env-msk-test')
    await page.fill('#root_target_cluster_id', 'lkc-msk-test')
    await page.fill('#root_target_rest_endpoint', 'https://msk-test.confluent.cloud:443')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-msk-test.confluent.cloud:9092')
    await page.fill('#root_cluster_link_name', 'msk-jump-iam-link')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test456')
    await page.fill('#root_jump_cluster_iam_auth_role_name', 'msk-cluster-link-role')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible()
    await page.click('button:has-text("Generate Terraform Files")')
    await page.waitForTimeout(2000)

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })
})
