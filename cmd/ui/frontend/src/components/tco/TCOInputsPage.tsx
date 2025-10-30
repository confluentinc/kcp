import { useState, useMemo, useEffect } from 'react'
import { Button } from '@/components/common/ui/button'
import { Modal } from '@/components/common/ui/modal'
import { useAppStore } from '@/stores/store'
import { ExternalLink } from 'lucide-react'
import ClusterMetrics from '@/components/explore/clusters/ClusterMetrics'
import { DEFAULTS } from '@/constants'

export default function TCOInputs() {
  const { regions, tcoWorkloadData, setTCOWorkloadValue, initializeTCOData } = useAppStore()

  // Get all clusters from all regions
  const allClusters = useMemo(() => {
    const clusters: Array<{ name: string; regionName: string; key: string }> = []
    regions.forEach((region) => {
      region.clusters?.forEach((cluster) => {
        clusters.push({
          name: cluster.name,
          regionName: region.name,
          key: `${region.name}:${cluster.name}`,
        })
      })
    })
    return clusters
  }, [regions])

  const [copySuccess, setCopySuccess] = useState(false)

  // Modal state for ClusterMetrics
  const [modalState, setModalState] = useState<{
    isOpen: boolean
    cluster: {
      name: string
      region: string
      metrics?: {
        metadata?: {
          start_date?: string
          end_date?: string
        }
      }
    } | null
    preselectedMetric: string | null
    workloadAssumption: string | null
  }>({
    isOpen: false,
    cluster: null,
    preselectedMetric: null,
    workloadAssumption: null,
  })

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

  // Open modal with cluster metrics and preselected metric
  const handleOpenMetricsModal = (
    clusterKey: string,
    metricType: 'avg-ingress' | 'peak-ingress' | 'avg-egress' | 'peak-egress' | 'partitions'
  ) => {
    const cluster = allClusters.find((c) => c.key === clusterKey)
    if (!cluster) return

    // Find the cluster object from regions
    const region = regions.find((r) => r.name === cluster.regionName)
    const clusterObj = region?.clusters?.find((c) => c.name === cluster.name)

    if (clusterObj && region) {
      let preselectedMetric: string
      let workloadAssumption: string

      switch (metricType) {
        case 'avg-ingress':
          preselectedMetric = 'BytesInPerSec'
          workloadAssumption = 'Avg Ingress Throughput (MB/s)'
          break
        case 'peak-ingress':
          preselectedMetric = 'BytesInPerSec'
          workloadAssumption = 'Peak Ingress Throughput (MB/s)'
          break
        case 'avg-egress':
          preselectedMetric = 'BytesOutPerSec'
          workloadAssumption = 'Avg Egress Throughput (MB/s)'
          break
        case 'peak-egress':
          preselectedMetric = 'BytesOutPerSec'
          workloadAssumption = 'Peak Egress Throughput (MB/s)'
          break
        case 'partitions':
          preselectedMetric = 'GlobalPartitionCount'
          workloadAssumption = 'Partitions'
          break
        default:
          preselectedMetric = 'BytesInPerSec'
          workloadAssumption = 'Ingress Throughput'
      }

      setModalState({
        isOpen: true,
        cluster: {
          name: clusterObj.name,
          region: region.name,
          metrics: clusterObj.metrics,
        },
        preselectedMetric,
        workloadAssumption,
      })
    }
  }

  // Close modal
  const handleCloseModal = () => {
    setModalState({
      isOpen: false,
      cluster: null,
      preselectedMetric: null,
      workloadAssumption: null,
    })
  }

  const generateCSV = () => {
    if (allClusters.length === 0) {
      return 'No clusters available. Please load a KCP state file first.'
    }

    // Create header row (just cluster names)
    const headers = allClusters.map((cluster) => cluster.name)

    // Create data rows (without workload assumption labels)
    const rows = [
      allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.avgIngressThroughput || ''),
      allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.peakIngressThroughput || ''),
      allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.avgEgressThroughput || ''),
      allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.peakEgressThroughput || ''),
      allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.retentionDays || ''),
      allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.partitions || DEFAULTS.PARTITIONS),
      allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.replicationFactor || DEFAULTS.REPLICATION_FACTOR),
      allClusters.map((cluster) => {
        // Find the cluster object from regions to get metadata
        const region = regions.find((r) => r.name === cluster.regionName)
        const clusterObj = region?.clusters?.find((c) => c.name === cluster.name)
        const followerFetching = clusterObj?.metrics?.metadata?.follower_fetching
        return followerFetching !== undefined ? followerFetching.toString().toUpperCase() : 'N/A'
      }),
      allClusters.map((cluster) => {
        // Find the cluster object from regions to get metadata
        const region = regions.find((r) => r.name === cluster.regionName)
        const clusterObj = region?.clusters?.find((c) => c.name === cluster.name)
        const tieredStorage = clusterObj?.metrics?.metadata?.tiered_storage
        return tieredStorage !== undefined ? tieredStorage.toString().toUpperCase() : 'N/A'
      }),
      allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.localRetentionHours || ''),
    ]

    // Combine headers and rows
    const csvContent = [headers, ...rows].map((row) => row.join(',')).join('\n')

    return csvContent
  }

  const copyToClipboard = async () => {
    try {
      const csvContent = generateCSV()
      if (csvContent.includes('No clusters available')) {
        return
      }
      await navigator.clipboard.writeText(csvContent)
      setCopySuccess(true)
      setTimeout(() => setCopySuccess(false), 1000) // Hide tick after 2 seconds
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
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Avg Ingress Throughput (MB/s)
                </td>
                {allClusters.map((cluster) => (
                  <td
                    key={cluster.key}
                    className="px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        step="0.01"
                        value={tcoWorkloadData[cluster.key]?.avgIngressThroughput || ''}
                        onChange={(e) =>
                          handleInputChange(cluster.key, 'avgIngressThroughput', e.target.value)
                        }
                        className="flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                        placeholder="0.00"
                      />
                      <Button
                        onClick={() => handleOpenMetricsModal(cluster.key, 'avg-ingress')}
                        variant="outline"
                        size="sm"
                        className="h-8 w-8 p-0 flex-shrink-0"
                        title="Go to cluster metrics for ingress data"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </Button>
                    </div>
                  </td>
                ))}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Peak Ingress Throughput (MB/s)
                </td>
                {allClusters.map((cluster) => (
                  <td
                    key={cluster.key}
                    className="px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        step="0.01"
                        value={tcoWorkloadData[cluster.key]?.peakIngressThroughput || ''}
                        onChange={(e) =>
                          handleInputChange(cluster.key, 'peakIngressThroughput', e.target.value)
                        }
                        className="flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                        placeholder="0.00"
                      />
                      <Button
                        onClick={() => handleOpenMetricsModal(cluster.key, 'peak-ingress')}
                        variant="outline"
                        size="sm"
                        className="h-8 w-8 p-0 flex-shrink-0"
                        title="Go to cluster metrics for ingress data"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </Button>
                    </div>
                  </td>
                ))}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Avg Egress Throughput (MB/s)
                </td>
                {allClusters.map((cluster) => (
                  <td
                    key={cluster.key}
                    className="px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        step="0.01"
                        value={tcoWorkloadData[cluster.key]?.avgEgressThroughput || ''}
                        onChange={(e) =>
                          handleInputChange(cluster.key, 'avgEgressThroughput', e.target.value)
                        }
                        className="flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                        placeholder="0.00"
                      />
                      <Button
                        onClick={() => handleOpenMetricsModal(cluster.key, 'avg-egress')}
                        variant="outline"
                        size="sm"
                        className="h-8 w-8 p-0 flex-shrink-0"
                        title="Go to cluster metrics for egress data"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </Button>
                    </div>
                  </td>
                ))}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Peak Egress Throughput (MB/s)
                </td>
                {allClusters.map((cluster) => (
                  <td
                    key={cluster.key}
                    className="px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        step="0.01"
                        value={tcoWorkloadData[cluster.key]?.peakEgressThroughput || ''}
                        onChange={(e) =>
                          handleInputChange(cluster.key, 'peakEgressThroughput', e.target.value)
                        }
                        className="flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                        placeholder="0.00"
                      />
                      <Button
                        onClick={() => handleOpenMetricsModal(cluster.key, 'peak-egress')}
                        variant="outline"
                        size="sm"
                        className="h-8 w-8 p-0 flex-shrink-0"
                        title="Go to cluster metrics for egress data"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </Button>
                    </div>
                  </td>
                ))}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Retention Days
                </td>
                {allClusters.map((cluster) => (
                  <td
                    key={cluster.key}
                    className="px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        step="1"
                        min="0"
                        value={tcoWorkloadData[cluster.key]?.retentionDays || ''}
                        onChange={(e) =>
                          handleInputChange(cluster.key, 'retentionDays', e.target.value)
                        }
                        className="flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                        placeholder="0"
                      />
                      <Button
                        disabled
                        variant="outline"
                        size="sm"
                        className="h-8 w-8 p-0 flex-shrink-0 opacity-50 cursor-not-allowed"
                        title="Feature coming soon"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </Button>
                    </div>
                  </td>
                ))}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Partitions
                </td>
                {allClusters.map((cluster) => (
                  <td
                    key={cluster.key}
                    className="px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        step="1"
                        min="0"
                        value={tcoWorkloadData[cluster.key]?.partitions || ''}
                        onChange={(e) =>
                          handleInputChange(cluster.key, 'partitions', e.target.value)
                        }
                        className="flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                        placeholder={DEFAULTS.PARTITIONS}
                      />
                      <Button
                        onClick={() => handleOpenMetricsModal(cluster.key, 'partitions')}
                        variant="outline"
                        size="sm"
                        className="h-8 w-8 p-0 flex-shrink-0"
                        title="Go to cluster metrics for partition data"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </Button>
                    </div>
                  </td>
                ))}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Replication Factor
                </td>
                {allClusters.map((cluster) => (
                  <td
                    key={cluster.key}
                    className="px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        step="1"
                        min="1"
                        value={tcoWorkloadData[cluster.key]?.replicationFactor || ''}
                        onChange={(e) =>
                          handleInputChange(cluster.key, 'replicationFactor', e.target.value)
                        }
                        className="flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                        placeholder={DEFAULTS.REPLICATION_FACTOR}
                      />
                      <Button
                        disabled
                        variant="outline"
                        size="sm"
                        className="h-8 w-8 p-0 flex-shrink-0 opacity-50 cursor-not-allowed"
                        title="Feature coming soon"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </Button>
                    </div>
                  </td>
                ))}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Follower Fetching
                </td>
                {allClusters.map((cluster) => {
                  // Find the cluster object from regions to get metadata
                  const region = regions.find((r) => r.name === cluster.regionName)
                  const clusterObj = region?.clusters?.find((c) => c.name === cluster.name)
                  const followerFetching = clusterObj?.metrics?.metadata?.follower_fetching

                  return (
                    <td
                      key={cluster.key}
                      className="px-4 py-3"
                    >
                      <div className="flex justify-center">
                        {followerFetching !== undefined ? (
                          <span
                            className={`inline-flex items-center justify-center w-6 h-6 rounded-full text-sm font-medium ${
                              followerFetching
                                ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                                : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                            }`}
                          >
                            {followerFetching ? '✓' : '✗'}
                          </span>
                        ) : (
                          <span className="text-sm text-gray-500 dark:text-gray-400">N/A</span>
                        )}
                      </div>
                    </td>
                  )
                })}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Tiered Storage
                </td>
                {allClusters.map((cluster) => {
                  // Find the cluster object from regions to get metadata
                  const region = regions.find((r) => r.name === cluster.regionName)
                  const clusterObj = region?.clusters?.find((c) => c.name === cluster.name)
                  const tieredStorage = clusterObj?.metrics?.metadata?.tiered_storage

                  return (
                    <td
                      key={cluster.key}
                      className="px-4 py-3"
                    >
                      <div className="flex justify-center">
                        {tieredStorage !== undefined ? (
                          <span
                            className={`inline-flex items-center justify-center w-6 h-6 rounded-full text-sm font-medium ${
                              tieredStorage
                                ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                                : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                            }`}
                          >
                            {tieredStorage ? '✓' : '✗'}
                          </span>
                        ) : (
                          <span className="text-sm text-gray-500 dark:text-gray-400">N/A</span>
                        )}
                      </div>
                    </td>
                  )
                })}
              </tr>
              <tr>
                <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
                  Local Retention in Primary Storage Hours
                </td>
                {allClusters.map((cluster) => (
                  <td
                    key={cluster.key}
                    className="px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="number"
                        step="1"
                        min="0"
                        value={tcoWorkloadData[cluster.key]?.localRetentionHours || ''}
                        onChange={(e) =>
                          handleInputChange(cluster.key, 'localRetentionHours', e.target.value)
                        }
                        className="flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
                        placeholder="0"
                      />
                      <Button
                        disabled
                        variant="outline"
                        size="sm"
                        className="h-8 w-8 p-0 flex-shrink-0 opacity-50 cursor-not-allowed"
                        title="Feature coming soon"
                      >
                        <ExternalLink className="h-3 w-3" />
                      </Button>
                    </div>
                  </td>
                ))}
              </tr>
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
          {generateCSV()}
        </pre>
      </div>

      {/* Cluster Metrics Modal */}
      <Modal
        isOpen={modalState.isOpen}
        onClose={handleCloseModal}
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
