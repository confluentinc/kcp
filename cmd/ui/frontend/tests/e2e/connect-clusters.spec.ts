import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json' with { type: 'json' }

// Build a state file with two self-managed Connect clusters (connect-a with a
// running connector that has metrics, connect-b with a connector that has none),
// injected into the known-valid OSK fixture so the rest of the cluster shape
// stays realistic. This exercises the `connect_clusters` shape (distinct from
// the older `self_managed_connectors` shape used by the redaction-warning spec).
function stateWithConnectClusters() {
  const state = structuredClone(stateOSKOnly) as Record<string, any>
  state.osk_sources.clusters[0].kafka_admin_client_information.connect_clusters = [
    {
      connect_rest_url: 'http://connect-a:8083',
      metrics: {
        metadata: {
          start_date: '2026-07-07T15:00:26Z',
          end_date: '2026-07-07T15:01:26Z',
          period: 10,
          metrics_source: 'jolokia',
        },
        results: [
          { start: '2026-07-07T15:00:26Z', end: '2026-07-07T15:00:36Z', label: 'task-count', value: 2 },
        ],
        aggregates: {},
        query_info: [],
      },
      connectors: [
        {
          name: 'datagen-a',
          state: 'RUNNING',
          config: {
            'connector.class': 'io.confluent.kafka.connect.datagen.DatagenConnector',
            'tasks.max': '1',
          },
          connect_host: '10.0.0.1:8083',
          metrics: {
            metadata: {
              start_date: '2026-07-07T15:00:26Z',
              end_date: '2026-07-07T15:01:26Z',
              period: 10,
              metrics_source: 'jolokia',
            },
            results: [
              {
                start: '2026-07-07T15:00:26Z',
                end: '2026-07-07T15:00:36Z',
                label: 'source-record-write-rate',
                value: 0.5,
              },
            ],
            aggregates: {},
            query_info: [],
          },
        },
      ],
    },
    {
      connect_rest_url: 'http://connect-b:8083',
      connectors: [
        {
          name: 's3-sink-b',
          state: 'RUNNING',
          config: { 'connector.class': 'io.confluent.connect.s3.S3SinkConnector' },
          connect_host: '10.0.0.2:8083',
        },
      ],
    },
  ]
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
  // Click auto-waits for the uploaded state to render the cluster button — no
  // fixed sleep. The role-scoped locator avoids matching the detail heading.
  await page.getByRole('button', { name: 'prod-kafka-cluster' }).click()
  await page.click('nav button:has-text("Connectors")')
}

test.describe('Self-managed Connect clusters', () => {
  test('drills from Connect-cluster dropdown through connector dropdown to details/metrics', async ({
    page,
  }) => {
    await openConnectorsView(page, stateWithConnectClusters())

    // The self-managed sub-tab is not active by default (MSK Connectors tab is);
    // switch to it before asserting on any self-managed-only testids.
    await page.getByRole('button', { name: /Self Managed Connectors/ }).click()

    const clusterSelect = page.getByTestId('connect-cluster-select')
    await expect(clusterSelect).toBeVisible()

    // --- Select the first Connect cluster (connect-a) ---
    await clusterSelect.click()
    await expect(page.getByRole('option', { name: 'http://connect-a:8083' })).toBeVisible()
    await expect(page.getByRole('option', { name: 'http://connect-b:8083' })).toBeVisible()
    await page.getByRole('option', { name: 'http://connect-a:8083' }).click()

    // --- Select its connector (datagen-a) ---
    const connectorSelect = page.getByTestId('connector-select')
    await connectorSelect.click()
    await expect(page.getByRole('option', { name: 'datagen-a' })).toBeVisible()
    await page.getByRole('option', { name: 'datagen-a' }).click()

    // --- Assert the selected connector's details render ---
    // Note: the config is displayed in a controlled, read-only <textarea>. React
    // sets a controlled textarea's content via the DOM `.value` property, not via
    // child text nodes, so it does NOT show up in `.textContent` (and therefore
    // not in Playwright's toContainText). Assert the connector.class value via
    // the textarea's `.value` (toHaveValue) instead; assert the rest (name,
    // worker host) via toContainText since those render as plain text nodes.
    const details = page.getByTestId('connector-details')
    await expect(details).toBeVisible()
    await expect(details).toContainText('datagen-a')
    await expect(details).toContainText('10.0.0.1:8083')
    await expect(details.locator('textarea')).toHaveValue(
      /connector\.class=io\.confluent\.kafka\.connect\.datagen\.DatagenConnector/
    )

    // --- Switch to the second Connect cluster (connect-b) ---
    await clusterSelect.click()
    await page.getByRole('option', { name: 'http://connect-b:8083' }).click()

    // --- Select its connector (s3-sink-b), which has no metrics ---
    await connectorSelect.click()
    await expect(page.getByRole('option', { name: 's3-sink-b' })).toBeVisible()
    await page.getByRole('option', { name: 's3-sink-b' }).click()

    await expect(page.getByTestId('connector-metrics-empty')).toBeVisible()

    await expect(details).toContainText('s3-sink-b')
    await expect(details).toContainText('10.0.0.2:8083')
    await expect(details.locator('textarea')).toHaveValue(
      /connector\.class=io\.confluent\.connect\.s3\.S3SinkConnector/
    )
  })
})
