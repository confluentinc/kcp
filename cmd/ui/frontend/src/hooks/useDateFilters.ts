import { useEffect, useState, useCallback } from 'react'
import type { ApiMetadata } from '@/types/api'

/**
 * Configuration for date filters with metadata
 */
export interface DateFiltersConfig {
  /** Start date value from state */
  startDate: Date | undefined
  /** End date value from state */
  endDate: Date | undefined
  /** Function to set start date */
  setStartDate: (date: Date | undefined) => void
  /** Function to set end date */
  setEndDate: (date: Date | undefined) => void
  /** Metadata from API response containing start_date and end_date */
  metadata?: ApiMetadata | null
  /** Optional callback when dates are reset (e.g., to reset chart zoom) */
  onReset?: () => void
  /** Whether to auto-set defaults from metadata (default: true) */
  autoSetDefaults?: boolean
}

/**
 * Return type for the hook
 */
export interface UseDateFiltersReturn {
  /** Start date value */
  startDate: Date | undefined
  /** End date value */
  endDate: Date | undefined
  /** Set start date */
  setStartDate: (date: Date | undefined) => void
  /** Set end date */
  setEndDate: (date: Date | undefined) => void
  /** Reset both dates to metadata values */
  resetToMetadataDates: () => void
  /** Reset start date to metadata value */
  resetStartDateToMetadata: () => void
  /** Reset end date to metadata value */
  resetEndDateToMetadata: () => void
  /** Whether metadata dates are available */
  hasMetadataDates: boolean
}

/**
 * Extracts metadata dates from a metadata object
 */
function getMetadataDates(metadata?: ApiMetadata | null): {
  startDate: string | null
  endDate: string | null
} {
  if (!metadata) {
    return { startDate: null, endDate: null }
  }

  return {
    startDate: metadata.start_date || null,
    endDate: metadata.end_date || null,
  }
}

/**
 * Hook to manage date filters with automatic default setting from metadata
 * and reset functions that use metadata dates.
 *
 * This hook eliminates duplication across components that need to:
 * - Set default dates from API metadata
 * - Provide reset functions that restore metadata dates
 *
 * @example
 * ```tsx
 * const { startDate, endDate, setStartDate, setEndDate, resetToMetadataDates } =
 *   useDateFilters({
 *     startDate,
 *     endDate,
 *     setStartDate,
 *     setEndDate,
 *     metadata: metricsResponse?.metadata,
 *     onReset: resetZoom,
 *   })
 * ```
 */
export function useDateFilters({
  startDate,
  endDate,
  setStartDate,
  setEndDate,
  metadata,
  onReset,
  autoSetDefaults = true,
}: DateFiltersConfig): UseDateFiltersReturn {
  const [defaultsSet, setDefaultsSet] = useState(false)

  // Extract metadata dates
  const metadataDates = getMetadataDates(metadata)
  const hasMetadataDates = Boolean(
    metadataDates.startDate && metadataDates.endDate
  )

  // Reset defaultsSet flag when dates are cleared (e.g., when switching clusters)
  useEffect(() => {
    if (!startDate && !endDate && defaultsSet) {
      setDefaultsSet(false)
    }
  }, [startDate, endDate, defaultsSet])

  // Set default dates from metadata when data is first loaded
  useEffect(() => {
    if (!autoSetDefaults || defaultsSet || !hasMetadataDates) return

    const metaStartDate = new Date(metadataDates.startDate!)
    const metaEndDate = new Date(metadataDates.endDate!)

    // Only set defaults if both dates are valid and no user selection has been made
    if (
      !startDate &&
      !endDate &&
      !isNaN(metaStartDate.getTime()) &&
      !isNaN(metaEndDate.getTime())
    ) {
      setStartDate(metaStartDate)
      setEndDate(metaEndDate)
      setDefaultsSet(true)
    }
  }, [
    autoSetDefaults,
    defaultsSet,
    hasMetadataDates,
    metadataDates.startDate,
    metadataDates.endDate,
    startDate,
    endDate,
    setStartDate,
    setEndDate,
  ])

  // Reset both dates to metadata values
  const resetToMetadataDates = useCallback(() => {
    if (!hasMetadataDates) return

    const metaStartDate = new Date(metadataDates.startDate!)
    const metaEndDate = new Date(metadataDates.endDate!)

    if (
      !isNaN(metaStartDate.getTime()) &&
      !isNaN(metaEndDate.getTime())
    ) {
      setStartDate(metaStartDate)
      setEndDate(metaEndDate)
      onReset?.()
    }
  }, [hasMetadataDates, metadataDates.startDate, metadataDates.endDate, setStartDate, setEndDate, onReset])

  // Reset start date to metadata value
  const resetStartDateToMetadata = useCallback(() => {
    if (!metadataDates.startDate) return

    const metaStartDate = new Date(metadataDates.startDate)
    if (!isNaN(metaStartDate.getTime())) {
      setStartDate(metaStartDate)
      onReset?.()
    }
  }, [metadataDates.startDate, setStartDate, onReset])

  // Reset end date to metadata value
  const resetEndDateToMetadata = useCallback(() => {
    if (!metadataDates.endDate) return

    const metaEndDate = new Date(metadataDates.endDate)
    if (!isNaN(metaEndDate.getTime())) {
      setEndDate(metaEndDate)
      onReset?.()
    }
  }, [metadataDates.endDate, setEndDate, onReset])

  return {
    startDate,
    endDate,
    setStartDate,
    setEndDate,
    resetToMetadataDates,
    resetStartDateToMetadata,
    resetEndDateToMetadata,
    hasMetadataDates,
  }
}

