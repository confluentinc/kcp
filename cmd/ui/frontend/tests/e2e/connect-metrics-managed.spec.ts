import { test, expect } from '@playwright/test'
import stateBoth from '../fixtures/state-both.json' with { type: 'json' }

// A minimal-but-valid MSK ConnectorSummary for the fixture (only the fields the
// connector summary card reads).
function connector(name: string) {
  return {
    connector_arn: `arn:aws:kafkaconnect:us-east-1:123456789012:connector/${name}/uuid`,
    connector_name: name,
    connector_state: 'RUNNING',
    creation_time: '2026-01-01T00:00:00Z',
    kafka_cluster: { BootstrapServers: 'b-1.example:9098', Vpc: { SecurityGroups: [], Subnets: [] } },
    kafka_cluster_client_authentication: { AuthenticationType: 'IAM' },
    capacity: { ProvisionedCapacity: { WorkerCount: 1, McuCount: 1 } },
    plugins: [],
    connector_configuration: { 'connector.class': 'io.example.Sink' },
  }
}

// Per-connector CloudWatch series, labeled "<metric> (<connectorName>)" exactly as
// the backend produces them, so the frontend's connector scoping (filter + strip)
// is exercised for real.
function connectorMetrics(connectorNames: string[]) {
  const results: Array<{ start: string; end: string; label: string; value: number }> = []
  const aggregates: Record<string, { min: number; avg: number; max: number }> = {}
  const query_info: Array<Record<string, unknown>> = []
  connectorNames.forEach((name, i) => {
    const base = (i + 1) * 100 // distinct values per connector
    for (const label of [`incoming-byte-rate (${name})`, `task-count (${name})`]) {
      results.push({ start: '2026-01-01T00:00:00Z', end: '2026-01-01T00:05:00Z', label, value: base })
      results.push({ start: '2026-01-01T00:05:00Z', end: '2026-01-01T00:10:00Z', label, value: base + 5 })
      aggregates[label] = { min: base, avg: base + 2.5, max: base + 5 }
      query_info.push({ metric_name: label, source_type: 'cloudwatch', namespace: 'AWS/KafkaConnect' })
    }
  })
  return {
    metadata: { start_date: '2026-01-01T00:00:00Z', end_date: '2026-01-01T01:00:00Z', period: 300, metrics_source: 'cloudwatch' },
    results,
    aggregates,
    query_info,
  }
}

function stateWithTwoConnectors() {
  const state = structuredClone(stateBoth) as Record<string, any>
  const cluster = state.msk_sources.regions[0].clusters[0]
  cluster.aws_client_information.connectors = [connector('connector-alpha'), connector('connector-beta')]
  cluster.aws_client_information.connector_metrics = connectorMetrics(['connector-alpha', 'connector-beta'])
  return state
}

async function openConnectorsView(page: import('@playwright/test').Page, state: unknown) {
  await page.goto('/')
  await page.click('button:has-text("Upload KCP State File")')
  await page.locator('input[type="file"]').setInputFiles({
    name: 'state.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(state)),
  })
  await page.getByRole('button', { name: 'msk-cluster-1' }).click()
  await page.click('nav button:has-text("Connectors")')
}

test.describe('MSK connector-scoped metrics', () => {
  test('scopes metrics + summary card to the connector chosen in the dropdown', async ({ page }) => {
    await openConnectorsView(page, stateWithTwoConnectors())

    // Header renamed for MSK-managed.
    await expect(page.getByRole('heading', { name: 'Connector Metrics' })).toBeVisible()

    // Defaults to the first connector: its metrics render and its summary card shows;
    // the other connector's card is not rendered.
    await expect(page.locator('.recharts-responsive-container')).toBeVisible()
    await expect(page.getByRole('heading', { name: 'connector-alpha' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'connector-beta' })).toHaveCount(0)

    // Switch the connector dropdown to the second connector.
    await page.getByRole('combobox').first().click()
    await page.getByRole('option', { name: 'connector-beta' }).click()

    // Now the second connector's summary card shows and the first's is gone.
    await expect(page.getByRole('heading', { name: 'connector-beta' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'connector-alpha' })).toHaveCount(0)
  })

  test('does not render a Connector Metrics block when connector_metrics is absent', async ({ page }) => {
    await openConnectorsView(page, stateBoth)
    await expect(page.getByRole('heading', { name: 'Connector Metrics' })).toHaveCount(0)
  })
})
