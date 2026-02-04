import type { ApiError } from '@/services/apiClient'

interface LagMonitorErrorProps {
  error: ApiError | Error
}

export const LagMonitorError = ({ error }: LagMonitorErrorProps) => {
  const isCredentialsError = error instanceof Error && 'status' in error && error.status === 400

  if (isCredentialsError) {
    return (
      <div className="max-w-4xl mx-auto p-6">
        <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded-lg p-6">
          <div className="flex items-start gap-4">
            <div className="flex-shrink-0">
              <span className="text-3xl">⚠️</span>
            </div>
            <div className="flex-1">
              <h2 className="text-xl font-semibold text-yellow-900 dark:text-yellow-200 mb-2">
                Cluster Link Credentials Not Configured
              </h2>
              <p className="text-yellow-800 dark:text-yellow-300 mb-4">
                The lag monitoring feature requires cluster link credentials to be provided when
                starting the UI.
              </p>
              <div className="bg-yellow-100 dark:bg-yellow-900/40 rounded-md p-4 mb-4">
                <p className="text-sm font-medium text-yellow-900 dark:text-yellow-200 mb-2">
                  To enable lag monitoring, restart the UI with the following flags:
                </p>
                <pre className="text-xs text-yellow-800 dark:text-yellow-300 overflow-x-auto">
                  {`kcp ui \\
  --rest-endpoint <endpoint> \\
  --cluster-id <cluster-id> \\
  --cluster-link-name <link-name> \\
  --cluster-api-key <api-key> \\
  --cluster-api-secret <api-secret>`}
                </pre>
              </div>
              <p className="text-sm text-yellow-700 dark:text-yellow-400">
                <strong>Note:</strong> These credentials are stored securely on the backend and are
                never sent to the frontend.
              </p>
            </div>
          </div>
        </div>
      </div>
    )
  }

  // Network or other errors
  return (
    <div className="max-w-4xl mx-auto p-6">
      <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-6">
        <div className="flex items-start gap-4">
          <div className="flex-shrink-0">
            <span className="text-3xl">❌</span>
          </div>
          <div className="flex-1">
            <h2 className="text-xl font-semibold text-red-900 dark:text-red-200 mb-2">
              Error Loading Lag Monitor Data
            </h2>
            <p className="text-red-800 dark:text-red-300 mb-2">
              {error.message || 'An unknown error occurred while fetching lag monitor data.'}
            </p>
            {'status' in error && error.status === 503 && (
              <p className="text-sm text-red-700 dark:text-red-400">
                The Confluent Cloud API is currently unreachable. Please check your network
                connection and cluster link configuration.
              </p>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
