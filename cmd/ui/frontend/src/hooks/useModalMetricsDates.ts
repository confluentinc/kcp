import { useState, useEffect, useRef } from 'react'

interface ModalMetricsDatesConfig {
  inModal: boolean
  isActive: boolean
  clusterName: string
  clusterRegion: string
  clusterMetadata?: {
    start_date?: string
    end_date?: string
  }
  metricsResponseMetadata?: {
    start_date?: string
    end_date?: string
  }
}

interface ModalMetricsDatesReturn {
  modalStartDate: Date | undefined
  modalEndDate: Date | undefined
  setModalStartDate: (date: Date | undefined) => void
  setModalEndDate: (date: Date | undefined) => void
}

/**
 * Hook to manage modal-specific date state and initialization logic.
 * Handles date initialization from cluster metadata and cleanup on cluster changes.
 */
export function useModalMetricsDates({
  inModal,
  isActive,
  clusterName,
  clusterRegion,
  clusterMetadata,
  metricsResponseMetadata,
}: ModalMetricsDatesConfig): ModalMetricsDatesReturn {
  const [modalStartDate, setModalStartDate] = useState<Date | undefined>(undefined)
  const [modalEndDate, setModalEndDate] = useState<Date | undefined>(undefined)
  const modalDatesResetRef = useRef(false)
  const previousModalStateRef = useRef(false)

  // Initialize modal dates from cluster metadata if available (from state file)
  useEffect(() => {
    if (inModal && clusterMetadata?.start_date && clusterMetadata?.end_date) {
      const metaStartDate = clusterMetadata.start_date
      const metaEndDate = clusterMetadata.end_date

      if (
        !isNaN(new Date(metaStartDate).getTime()) &&
        !isNaN(new Date(metaEndDate).getTime()) &&
        !modalStartDate &&
        !modalEndDate
      ) {
        setModalStartDate(new Date(metaStartDate))
        setModalEndDate(new Date(metaEndDate))
      }
    }
  }, [
    inModal,
    clusterMetadata?.start_date,
    clusterMetadata?.end_date,
    modalStartDate,
    modalEndDate,
  ])

  // Reset modal dates when cluster changes
  useEffect(() => {
    modalDatesResetRef.current = false
    if (inModal) {
      setModalStartDate(undefined)
      setModalEndDate(undefined)
    }
  }, [clusterName, clusterRegion, inModal])

  // Reset dates to metadata when opened in modal mode (use local state, not store)
  useEffect(() => {
    const isModalActive = inModal && isActive

    // Track previous modal state
    const wasModalActive = previousModalStateRef.current
    previousModalStateRef.current = isModalActive

    // Reset local state when modal closes
    if (!isModalActive) {
      modalDatesResetRef.current = false
      setModalStartDate(undefined)
      setModalEndDate(undefined)
      return
    }

    // When modal opens (transitions from closed to open), reset the flag so dates can be set
    if (isModalActive && !wasModalActive) {
      modalDatesResetRef.current = false
    }

    // Set dates to metadata values whenever modal is active and metadata is available
    // This handles both initial open and when metadata loads after fetch
    if (isModalActive && !modalDatesResetRef.current && metricsResponseMetadata) {
      const metaStartDate = metricsResponseMetadata.start_date
      const metaEndDate = metricsResponseMetadata.end_date

      if (
        metaStartDate &&
        metaEndDate &&
        !isNaN(new Date(metaStartDate).getTime()) &&
        !isNaN(new Date(metaEndDate).getTime())
      ) {
        // Mark that we've reset to prevent re-running
        modalDatesResetRef.current = true

        // Reset local modal dates to metadata values (not store)
        setModalStartDate(new Date(metaStartDate))
        setModalEndDate(new Date(metaEndDate))
      }
    }
  }, [inModal, isActive, metricsResponseMetadata])

  return {
    modalStartDate,
    modalEndDate,
    setModalStartDate,
    setModalEndDate,
  }
}
