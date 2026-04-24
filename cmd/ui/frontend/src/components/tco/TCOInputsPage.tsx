import { useState, useEffect } from 'react'
import { Button } from '@/components/common/ui/button'
import { Modal } from '@/components/common/ui/modal'
import { useAppStore, useRegions } from '@/stores/store'
import { ClusterMetrics } from '@/components/explore/clusters/ClusterMetrics'
import { DEFAULTS } from '@/constants'
import { useTCOClusters } from '@/hooks/useTCOClusters'
import { useOSKTCOClusters } from '@/hooks/useOSKTCOClusters'
import { useTCOModal } from '@/hooks/useTCOModal'
import { generateTCOCSV } from '@/lib/tcoUtils'
import { TCOInputRow } from './TCOInputRow'

type SourceTab = 'msk' | 'osk'

export const TCOInputs = () => {
  const regions = useRegions()
  const tcoWorkloadData = useAppStore((state) => state.tcoWorkloadData)
  const setTCOWorkloadValue = useAppStore((state) => state.setTCOWorkloadValue)
  const initializeTCOData = useAppStore((state) => state.initializeTCOData)

  const mskClusters = useTCOClusters()
  const oskClusters = useOSKTCOClusters()

  const [activeTab, setActiveTab] = useState<SourceTab>('msk')
  const activeClusters = activeTab === 'msk' ? mskClusters : oskClusters

  const { modalState, openModal, closeModal } = useTCOModal(activeClusters)

  const [copySuccess, setCopySuccess] = useState(false)

  useEffect(() => {
    initializeTCOData(mskClusters)
  }, [mskClusters, initializeTCOData])

  useEffect(() => {
    initializeTCOData(oskClusters)
  }, [oskClusters, initializeTCOData])

  const handleInputChange = (
    clusterKey: string,
    field:
      | 'avgIngressThroughput'
      | 'peakIngressThroughput'
      | 'avgEgressThroughput'
      | 'peakEgressThroughput'
      | 'retentionDays'
      | 'partitions'
      | 'replicationFactor'
      | 'localRetentionHours',
    value: string
  ) => {
    setTCOWorkloadValue(clusterKey, field, value)
  }

  const handleOpenMetricsModal = (clusterKey: string, metricType: string) => {
    openModal(
      clusterKey,
      metricType as 'avg-ingress' | 'peak-ingress' | 'avg-egress' | 'peak-egress' | 'partitions',
      activeTab
    )
  }

  const copyToClipboard = async () => {
    try {
      const csvContent = generateTCOCSV(activeClusters, tcoWorkloadData, regions)
      if (csvContent.includes('No clusters available')) {
        return
      }
      await navigator.clipboard.writeText(csvContent)
      setCopySuccess(true)
      setTimeout(() => setCopySuccess(false), 1000)
    } catch {
      // Failed to copy to clipboard
    }
  }

  const tabs: { id: SourceTab; label: string; count: number }[] = [
    { id: 'msk', label: 'MSK', count: mskClusters.length },
    { id: 'osk', label: 'OSK', count: oskClusters.length },
  ]

  const renderColumnHeader = (cluster: typeof activeClusters[number]) => {
    if (cluster.sourceType === 'osk' && cluster.metadata) {
      const meta = cluster.metadata
      return (
        <div className="flex flex-col">
          <span className="font-semibold">{cluster.name}</span>
          <div className="text-xs text-muted-foreground font-normal mt-1 leading-relaxed">
            {meta.environment && (
              <div><span className="opacity-60">env:</span> {meta.environment}</div>
            )}
            {meta.location && (
              <div><span className="opacity-60">loc:</span> {meta.location}</div>
            )}
            {meta.kafka_version && (
              <div><span className="opacity-60">ver:</span> {meta.kafka_version}</div>
            )}
          </div>
          {meta.labels && Object.keys(meta.labels).length > 0 && (
            <div className="flex flex-wrap gap-1 mt-1">
              {Object.entries(meta.labels).map(([k, v]) => (
                <span
                  key={k}
                  className="inline-block px-1.5 py-0.5 rounded text-[10px] bg-secondary border border-border"
                >
                  {k}: {v}
                </span>
              ))}
            </div>
          )}
          <span className="inline-block mt-1.5 px-1.5 py-0.5 rounded text-[10px] font-semibold bg-blue-500/20 text-blue-500 border border-blue-500/30 w-fit">
            OSK
          </span>
        </div>
      )
    }

    return (
      <div className="flex flex-col">
        <span className="font-semibold">{cluster.name}</span>
        <span className="text-xs text-muted-foreground font-normal">
          {cluster.regionName}
        </span>
        {cluster.sourceType === 'msk' && (
          <span className="inline-block mt-1.5 px-1.5 py-0.5 rounded text-[10px] font-semibold bg-amber-500/20 text-amber-500 border border-amber-500/30 w-fit">
            MSK
          </span>
        )}
      </div>
    )
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-foreground">TCO Inputs</h1>
      </div>

      {/* Source type tabs */}
      <div className="flex gap-0 mb-4 border-b-2 border-border">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`px-5 py-2 text-sm font-medium -mb-[2px] border-b-2 transition-colors ${
              activeTab === tab.id
                ? 'border-primary text-primary'
                : 'border-transparent text-muted-foreground hover:text-foreground'
            }`}
          >
            {tab.label}
            <span
              className={`ml-2 px-1.5 py-0.5 rounded-full text-xs ${
                activeTab === tab.id
                  ? 'bg-primary/10 text-primary'
                  : 'bg-secondary text-muted-foreground'
              }`}
            >
              {tab.count}
            </span>
          </button>
        ))}
      </div>

      {activeClusters.length === 0 ? (
        <div className="bg-warning/10 border border-warning/20 rounded-md p-4">
          <p className="text-warning">
            No {activeTab === 'msk' ? 'MSK' : 'OSK'} clusters found. Upload a KCP state file with {activeTab === 'msk' ? 'MSK' : 'OSK'} cluster data.
          </p>
        </div>
      ) : (
        <>
          <div className="bg-card rounded-lg border border-border overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="bg-secondary border-b border-border">
                    <th className="px-4 py-3 text-left text-sm font-medium text-foreground min-w-[200px]">
                      Workload Assumptions
                    </th>
                    {activeClusters.map((cluster) => (
                      <th
                        key={cluster.key}
                        className="px-4 py-3 text-left text-sm font-medium text-foreground min-w-[150px]"
                      >
                        {renderColumnHeader(cluster)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200 dark:divide-gray-600">
                  <TCOInputRow
                    label="Avg Ingress Throughput (MB/s)"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    field="avgIngressThroughput"
                    onInputChange={handleInputChange}
                    onMetricsClick={handleOpenMetricsModal}
                    metricType="avg-ingress"
                    step="0.01"
                    placeholder="0.00"
                    buttonTitle="Go to cluster metrics for ingress data"
                  />
                  <TCOInputRow
                    label="Peak Ingress Throughput (MB/s)"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    field="peakIngressThroughput"
                    onInputChange={handleInputChange}
                    onMetricsClick={handleOpenMetricsModal}
                    metricType="peak-ingress"
                    step="0.01"
                    placeholder="0.00"
                    buttonTitle="Go to cluster metrics for ingress data"
                  />
                  <TCOInputRow
                    label="Avg Egress Throughput (MB/s)"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    field="avgEgressThroughput"
                    onInputChange={handleInputChange}
                    onMetricsClick={handleOpenMetricsModal}
                    metricType="avg-egress"
                    step="0.01"
                    placeholder="0.00"
                    buttonTitle="Go to cluster metrics for egress data"
                  />
                  <TCOInputRow
                    label="Peak Egress Throughput (MB/s)"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    field="peakEgressThroughput"
                    onInputChange={handleInputChange}
                    onMetricsClick={handleOpenMetricsModal}
                    metricType="peak-egress"
                    step="0.01"
                    placeholder="0.00"
                    buttonTitle="Go to cluster metrics for egress data"
                  />
                  <TCOInputRow
                    label="Retention Days"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    field="retentionDays"
                    onInputChange={handleInputChange}
                    step="1"
                    min="0"
                    placeholder="0"
                    buttonDisabled={true}
                    buttonTitle="Feature coming soon"
                  />
                  <TCOInputRow
                    label="Partitions"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    field="partitions"
                    onInputChange={handleInputChange}
                    onMetricsClick={handleOpenMetricsModal}
                    metricType="partitions"
                    step="1"
                    min="0"
                    placeholder={DEFAULTS.PARTITIONS}
                    buttonTitle="Go to cluster metrics for partition data"
                  />
                  <TCOInputRow
                    label="Replication Factor"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    field="replicationFactor"
                    onInputChange={handleInputChange}
                    step="1"
                    min="1"
                    placeholder={DEFAULTS.REPLICATION_FACTOR}
                    buttonDisabled={true}
                    buttonTitle="Feature coming soon"
                  />
                  <TCOInputRow
                    label="Follower Fetching"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    readOnly={true}
                    readOnlyValue={(cluster, oskCluster) => {
                      if (oskCluster) return undefined
                      return cluster?.metrics?.metadata?.follower_fetching
                    }}
                  />
                  <TCOInputRow
                    label="Tiered Storage"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    readOnly={true}
                    readOnlyValue={(cluster, oskCluster) => {
                      if (oskCluster) return undefined
                      return cluster?.metrics?.metadata?.tiered_storage
                    }}
                  />
                  <TCOInputRow
                    label="Local Retention in Primary Storage Hours"
                    clusters={activeClusters}
                    tcoWorkloadData={tcoWorkloadData}
                    regions={regions}
                    field="localRetentionHours"
                    onInputChange={handleInputChange}
                    step="1"
                    min="0"
                    placeholder="0"
                    buttonDisabled={true}
                    buttonTitle="Feature coming soon"
                  />
                </tbody>
              </table>
            </div>
          </div>

          <div className="mt-6 p-4 bg-secondary rounded-lg border border-border">
            <div className="flex justify-between items-center mb-2">
              <h3 className="text-sm font-medium text-foreground">CSV Preview:</h3>
              <Button
                onClick={copyToClipboard}
                variant="outline"
                size="sm"
                className={`transition-colors ${copySuccess ? 'border-green-600 text-green-600' : ''}`}
              >
                {copySuccess ? (
                  <span className="flex items-center space-x-1">
                    <span>{'✓'}</span>
                    <span>Copied!</span>
                  </span>
                ) : (
                  'Copy CSV'
                )}
              </Button>
            </div>
            <pre className="text-xs text-muted-foreground whitespace-pre-wrap font-mono bg-card p-3 rounded border">
              {generateTCOCSV(activeClusters, tcoWorkloadData, regions)}
            </pre>
          </div>
        </>
      )}

      {/* Cluster Metrics Modal */}
      <Modal
        isOpen={modalState.isOpen}
        onClose={closeModal}
        title={`Metrics - ${modalState.cluster?.name || 'Cluster'}`}
      >
        {modalState.cluster && (
          <ClusterMetrics
            cluster={{
              ...modalState.cluster,
              arn: modalState.cluster.sourceType === 'msk' ? modalState.cluster.key : undefined,
            }}
            isActive={modalState.isOpen}
            inModal={true}
            sourceType={modalState.cluster.sourceType}
            clusterId={modalState.cluster.sourceType === 'osk' ? modalState.cluster.key : undefined}
            modalPreselectedMetric={modalState.preselectedMetric || undefined}
            modalWorkloadAssumption={modalState.workloadAssumption || undefined}
          />
        )}
      </Modal>
    </div>
  )
}
