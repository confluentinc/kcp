import { useEffect, useState, useRef } from 'react'
import { apiClient, ApiError } from '@/services/apiClient'
import type { MirrorTopic, LagMonitorConfig } from '@/types/api'
import { LagMonitorTable } from './LagMonitorTable'
import { LagMonitorError } from './LagMonitorError'

const MAX_HISTORY_LENGTH = 30 // Keep last 30 data points

export const LagMonitor = () => {
  const [mirrorTopics, setMirrorTopics] = useState<MirrorTopic[] | null>(null)
  const [config, setConfig] = useState<LagMonitorConfig | null>(null)
  const [error, setError] = useState<ApiError | Error | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [lagHistory, setLagHistory] = useState<Map<string, number[]>>(new Map())
  const [shouldPoll, setShouldPoll] = useState(true)
  const [pollInterval, setPollInterval] = useState(5) // in seconds
  const [isPausedByError, setIsPausedByError] = useState(false)
  const shouldPollRef = useRef(true)
  const intervalRef = useRef<NodeJS.Timeout | null>(null)

  const fetchLagStatus = async () => {
    try {
      const data = await apiClient.lagMonitor.getLagStatus()
      setMirrorTopics(data)
      setError(null)
      setLastUpdated(new Date())

      // Resume polling on successful fetch
      shouldPollRef.current = true
      setShouldPoll(true)
      setIsPausedByError(false)

      // Update lag history
      setLagHistory((prevHistory) => {
        const newHistory = new Map(prevHistory)

        data.forEach((topic) => {
          const totalLag = topic.mirror_lags.reduce((sum, l) => sum + l.lag, 0)
          const history = newHistory.get(topic.mirror_topic_name) || []
          const updatedHistory = [...history, totalLag].slice(-MAX_HISTORY_LENGTH)
          newHistory.set(topic.mirror_topic_name, updatedHistory)
        })

        return newHistory
      })
    } catch (err) {
      console.error('Error fetching lag monitor data:', err)
      setError(err as ApiError | Error)

      // Stop polling for terminal errors (missing credentials, invalid credentials, or service unavailable)
      if (
        err instanceof ApiError &&
        (err.status === 400 || err.status === 401 || err.status === 403 || err.status === 503)
      ) {
        setMirrorTopics(null)
        shouldPollRef.current = false
        setShouldPoll(false)
        setIsPausedByError(true)
      }
    } finally {
      setIsLoading(false)
    }
  }

  const fetchConfig = async () => {
    try {
      const configData = await apiClient.lagMonitor.getConfig()
      setConfig(configData)
    } catch (err) {
      console.error('Error fetching lag monitor config:', err)
      // Config errors are non-fatal, just log them
    }
  }

  useEffect(() => {
    // Fetch configuration on mount
    fetchConfig()

    // Initial fetch
    fetchLagStatus()
  }, [])

  useEffect(() => {
    // Clear any existing interval
    if (intervalRef.current) {
      clearInterval(intervalRef.current)
    }

    // Set up new polling interval with selected duration
    intervalRef.current = setInterval(() => {
      // Only fetch if we should still be polling
      if (shouldPollRef.current) {
        fetchLagStatus()
      }
    }, pollInterval * 1000) // Convert seconds to milliseconds

    // Cleanup
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current)
      }
    }
  }, [pollInterval])

  const handleTogglePause = () => {
    const newShouldPoll = !shouldPoll
    shouldPollRef.current = newShouldPoll
    setShouldPoll(newShouldPoll)
    setIsPausedByError(false) // Manual pause/resume, not due to error

    // If resuming, fetch immediately
    if (newShouldPoll) {
      fetchLagStatus()
    }
  }

  // Show error if present
  if (error && (!mirrorTopics || mirrorTopics.length === 0)) {
    return <LagMonitorError error={error} />
  }

  // Show loading state on initial load
  if (isLoading && !mirrorTopics) {
    return (
      <div className="flex items-center justify-center h-full p-12">
        <div className="text-center">
          <div className="inline-block animate-spin rounded-full h-12 w-12 border-b-2 border-accent mb-4"></div>
          <p className="text-gray-600 dark:text-gray-400">Loading lag monitor data...</p>
        </div>
      </div>
    )
  }

  return (
    <div className="h-full">
      {/* Header */}
      <div className="bg-white dark:bg-card border-b border-gray-200 dark:border-border px-6 py-4">
        <div className="flex items-start justify-between gap-6">
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2">
              <label
                htmlFor="poll-interval"
                className="text-sm text-gray-600 dark:text-gray-400"
              >
                Refresh every:
              </label>
              <select
                id="poll-interval"
                value={pollInterval}
                onChange={(e) => setPollInterval(Number(e.target.value))}
                disabled={isPausedByError}
                className="text-sm border border-gray-300 dark:border-border rounded-md px-3 py-1.5 bg-white dark:bg-card text-gray-700 dark:text-gray-300 focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {[1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map((seconds) => (
                  <option
                    key={seconds}
                    value={seconds}
                  >
                    {seconds} {seconds === 1 ? 'second' : 'seconds'}
                  </option>
                ))}
              </select>
            </div>
            <button
              onClick={handleTogglePause}
              disabled={isPausedByError}
              className="inline-flex items-center px-4 py-2 border border-gray-300 dark:border-border rounded-md shadow-sm text-sm font-medium text-gray-700 dark:text-gray-300 bg-white dark:bg-card hover:bg-gray-50 dark:hover:bg-gray-800 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-accent disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {shouldPoll ? (
                <>
                  <span className="mr-2">⏸</span>
                  Pause
                </>
              ) : (
                <>
                  <span className="mr-2">▶</span>
                  Resume
                </>
              )}
            </button>
            {lastUpdated && (
              <div className="text-sm text-gray-600 dark:text-gray-400">
                Last updated: {lastUpdated.toLocaleTimeString()}
              </div>
            )}
          </div>
          {config && (
            <div className="inline-flex flex-col gap-1.5 bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 rounded-lg px-4 py-2.5">
              <div className="flex items-baseline gap-2">
                <span className="text-xs font-semibold text-green-700 dark:text-green-300 min-w-[110px]">
                  REST Endpoint:
                </span>
                <span className="text-xs text-green-900 dark:text-green-200 font-mono">
                  {config.rest_endpoint}
                </span>
              </div>
              <div className="flex items-baseline gap-2">
                <span className="text-xs font-semibold text-green-700 dark:text-green-300 min-w-[110px]">
                  Cluster ID:
                </span>
                <span className="text-xs text-green-900 dark:text-green-200 font-mono">
                  {config.cluster_id}
                </span>
              </div>
              <div className="flex items-baseline gap-2">
                <span className="text-xs font-semibold text-green-700 dark:text-green-300 min-w-[110px]">
                  Link Name:
                </span>
                <span className="text-xs text-green-900 dark:text-green-200 font-mono">
                  {config.cluster_link_name}
                </span>
              </div>
            </div>
          )}
        </div>

        {/* Auto-refresh indicator */}
        {shouldPoll ? (
          <div className="mt-3 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span className="inline-block w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
            Auto-refreshing every {pollInterval} {pollInterval === 1 ? 'second' : 'seconds'}
          </div>
        ) : isPausedByError ? (
          <div className="mt-3 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span className="inline-block w-2 h-2 bg-red-500 rounded-full"></span>
            Auto-refresh paused due to error
          </div>
        ) : (
          <div className="mt-3 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span className="inline-block w-2 h-2 bg-yellow-500 rounded-full"></span>
            Auto-refresh paused
          </div>
        )}
      </div>

      {/* Table */}
      <div
        className="overflow-auto"
        style={{ height: 'calc(100% - 140px)' }}
      >
        {mirrorTopics && (
          <LagMonitorTable
            mirrorTopics={mirrorTopics}
            lagHistory={lagHistory}
          />
        )}
      </div>
    </div>
  )
}
