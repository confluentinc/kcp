interface ErrorDisplayProps {
  title?: string
  error: string
  context?: string
}

/**
 * Reusable error display component for consistent error UI across the application
 */
export default function ErrorDisplay({ title, error, context = 'data' }: ErrorDisplayProps) {
  return (
    <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6 transition-colors">
      {title && (
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">{title}</h3>
      )}
      <div className="text-red-500 dark:text-red-400">
        <p className="font-medium">Error loading {context}:</p>
        <p className="text-sm mt-1">{error}</p>
      </div>
    </div>
  )
}
