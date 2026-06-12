import { useState, useEffect, useRef, useCallback } from 'react'

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

export const useMetricSelection = ({
  availableMetrics,
  inModal,
  modalPreselectedMetric,
  preselectedMetric,
  clusterName,
  clusterRegion,
}: MetricSelectionConfig): MetricSelectionReturn => {
  const [selectedMetric, setSelectedMetricRaw] = useState<string>('')
  const [hasUsedPreselectedMetric, setHasUsedPreselectedMetric] = useState(false)
  const userOverrodePreselection = useRef(false)

  const setSelectedMetric = useCallback((metric: string) => {
    if (inModal && modalPreselectedMetric && metric !== modalPreselectedMetric) {
      userOverrodePreselection.current = true
    }
    setSelectedMetricRaw(metric)
  }, [inModal, modalPreselectedMetric])

  useEffect(() => {
    setHasUsedPreselectedMetric(false)
    userOverrodePreselection.current = false
  }, [clusterName, clusterRegion])

  useEffect(() => {
    if (availableMetrics.length > 0) {
      if (inModal && modalPreselectedMetric && !userOverrodePreselection.current) {
        setSelectedMetricRaw(modalPreselectedMetric)
      } else if (
        !inModal &&
        preselectedMetric &&
        availableMetrics.includes(preselectedMetric) &&
        !hasUsedPreselectedMetric
      ) {
        setSelectedMetricRaw(preselectedMetric)
        setHasUsedPreselectedMetric(true)
      } else if (!selectedMetric) {
        setSelectedMetricRaw(availableMetrics[0])
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
    !userOverrodePreselection.current

  return {
    selectedMetric,
    setSelectedMetric,
    preselectedMetricMissing,
  }
}
