import { useState, useEffect } from 'react'
import { Button } from '@/components/common/ui/button'
import { Modal } from '@/components/common/ui/modal'
import { useAppStore, useRegions } from '@/stores/store'
import { ClusterMetrics } from '@/components/explore/clusters/ClusterMetrics'
import { DEFAULTS } from '@/constants'
import { useTCOClusters } from '@/hooks/useTCOClusters'
import { useTCOModal } from '@/hooks/useTCOModal'
import { generateTCOCSV } from '@/lib/tcoUtils'
import { TCOInputRow } from './TCOInputRow'

export const TCOInputs = () => {
  const regions = useRegions()
  const tcoWorkloadData = useAppStore((state) => state.tcoWorkloadData)
  const setTCOWorkloadValue = useAppStore((state) => state.setTCOWorkloadValue)
  const initializeTCOData = useAppStore((state) => state.initializeTCOData)

  const allClusters = useTCOClusters()
  const { modalState, openModal, closeModal } = useTCOModal(allClusters)

  const [copySuccess, setCopySuccess] = useState(false)

  // Initialize TCO data when clusters change
  useEffect(() => {
    initializeTCOData(allClusters)
  }, [allClusters, initializeTCOData])

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
      metricType as 'avg-ingress' | 'peak-ingress' | 'avg-egress' | 'peak-egress' | 'partitions'
    )
  }

  const copyToClipboard = async () => {
    try {
      const csvContent = generateTCOCSV(allClusters, tcoWorkloadData, regions)
      if (csvContent.includes('No clusters available')) {
        return
      }
      await navigator.clipboard.writeText(csvContent)
      setCopySuccess(true)
      setTimeout(() => setCopySuccess(false), 1000)
    } catch {
      // Failed to copy to clipboard - silently fail as this is not critical
    }
  }

  if (allClusters.length === 0) {
    return (
      <div className="p-6">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100 mb-4">TCO Inputs</h1>
        <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-border rounded-md p-4">
          <p className="text-yellow-800 dark:text-yellow-200">
            No clusters available. Please upload a KCP state file first to see the TCO input form.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">TCO Inputs</h1>
      </div>

      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="bg-gray-50 dark:bg-card border-b border-gray-200 dark:border-border">
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 min-w-[200px]">
                  Workload Assumptions
                </th>
                {allClusters.map((cluster) => (
                  <th
                    key={cluster.key}
                    className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 min-w-[150px]"
                  >
                    <div className="flex flex-col">
                      <span className="font-semibold">{cluster.name}</span>
                      <span className="text-xs text-gray-500 dark:text-gray-400 font-normal">
                        {cluster.regionName}
                      </span>
                    </div>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-200 dark:divide-gray-600">
              <TCOInputRow
                label="Avg Ingress Throughput (MB/s)"
                clusters={allClusters}
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
                clusters={allClusters}
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
                clusters={allClusters}
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
                clusters={allClusters}
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
                clusters={allClusters}
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
                clusters={allClusters}
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
                clusters={allClusters}
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
                clusters={allClusters}
                tcoWorkloadData={tcoWorkloadData}
                regions={regions}
                readOnly={true}
                readOnlyValue={(cluster) => cluster?.metrics?.metadata?.follower_fetching}
              />
              <TCOInputRow
                label="Tiered Storage"
                clusters={allClusters}
                tcoWorkloadData={tcoWorkloadData}
                regions={regions}
                readOnly={true}
                readOnlyValue={(cluster) => cluster?.metrics?.metadata?.tiered_storage}
              />
              <TCOInputRow
                label="Local Retention in Primary Storage Hours"
                clusters={allClusters}
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

      <div className="mt-6 p-4 bg-gray-50 dark:bg-card rounded-lg border border-gray-200 dark:border-border">
        <div className="flex justify-between items-center mb-2">
          <h3 className="text-sm font-medium text-gray-900 dark:text-gray-100">CSV Preview:</h3>
          <Button
            onClick={copyToClipboard}
            variant="outline"
            size="sm"
            className={`transition-colors ${copySuccess ? 'border-green-600 text-green-600' : ''}`}
          >
            {copySuccess ? (
              <span className="flex items-center space-x-1">
                <span>✓</span>
                <span>Copied!</span>
              </span>
            ) : (
              'Copy CSV'
            )}
          </Button>
        </div>
        <pre className="text-xs text-gray-600 dark:text-gray-400 whitespace-pre-wrap font-mono bg-white dark:bg-card p-3 rounded border">
          {generateTCOCSV(allClusters, tcoWorkloadData, regions)}
        </pre>
      </div>

      {/* Cluster Metrics Modal */}
      <Modal
        isOpen={modalState.isOpen}
        onClose={closeModal}
        title={`Metrics - ${modalState.cluster?.name || 'Cluster'}`}
      >
        {modalState.cluster && (
          <ClusterMetrics
            cluster={modalState.cluster}
            isActive={modalState.isOpen}
            inModal={true}
            modalPreselectedMetric={modalState.preselectedMetric || undefined}
            modalWorkloadAssumption={modalState.workloadAssumption || undefined}
          />
        )}
      </Modal>
    </div>
  )
}
