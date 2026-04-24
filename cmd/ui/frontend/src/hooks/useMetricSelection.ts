import { useState, useEffect } from 'react'

interface MetricSelectionConfig {
  availableMetrics: string[]
  inModal: boolean
  modalPreselectedMetric?: string
  preselectedMetric: string | null
  clusterName: string
  clusterRegion: string
}

interface MetricSelectionReturn {
  selectedMetric: string
  setSelectedMetric: (metric: string) => void
  preselectedMetricMissing: boolean
}

/**
 * Hook to manage metric selection with support for preselected metrics.
 * Handles both modal and non-modal preselection, and resets on cluster change.
 */
export const useMetricSelection = ({
  availableMetrics,
  inModal,
  modalPreselectedMetric,
  preselectedMetric,
  clusterName,
  clusterRegion,
}: MetricSelectionConfig): MetricSelectionReturn => {
  const [selectedMetric, setSelectedMetric] = useState<string>('')
  const [hasUsedPreselectedMetric, setHasUsedPreselectedMetric] = useState(false)

  // Reset preselected metric flag when cluster changes
  useEffect(() => {
    setHasUsedPreselectedMetric(false)
  }, [clusterName, clusterRegion])

  // Set default selected metric when data loads, prioritizing modal preselected metric
  useEffect(() => {
    if (availableMetrics.length > 0) {
      if (inModal && modalPreselectedMetric) {
        setSelectedMetric(modalPreselectedMetric)
      } else if (
        !inModal &&
        preselectedMetric &&
        availableMetrics.includes(preselectedMetric) &&
        !hasUsedPreselectedMetric
      ) {
        setSelectedMetric(preselectedMetric)
        setHasUsedPreselectedMetric(true)
      } else if (!selectedMetric) {
        setSelectedMetric(availableMetrics[0])
      }
    }
  }, [
    availableMetrics,
    selectedMetric,
    preselectedMetric,
    hasUsedPreselectedMetric,
    inModal,
    modalPreselectedMetric,
  ])

  const preselectedMetricMissing =
    inModal &&
    !!modalPreselectedMetric &&
    availableMetrics.length > 0 &&
    !availableMetrics.includes(modalPreselectedMetric) &&
    selectedMetric === modalPreselectedMetric

  return {
    selectedMetric,
    setSelectedMetric,
    preselectedMetricMissing,
  }
}
