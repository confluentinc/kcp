import { Component, type ReactNode } from 'react'

interface ErrorBoundaryProps {
  children: ReactNode
  fallback?: ReactNode
  onError?: (error: Error, errorInfo: React.ErrorInfo) => void
}

interface ErrorBoundaryState {
  hasError: boolean
  error: Error | null
}

/**
 * Error Boundary component for catching React errors and displaying a fallback UI
 * 
 * @example
 * ```tsx
 * <ErrorBoundary fallback={<ErrorFallback />}>
 *   <YourComponent />
 * </ErrorBoundary>
 * ```
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props)
    this.state = {
      hasError: false,
      error: null,
    }
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return {
      hasError: true,
      error,
    }
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    // Call optional error handler
    this.props.onError?.(error, errorInfo)
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback
      }

      return (
        <div className="flex items-center justify-center min-h-[400px] p-6">
          <div className="text-center max-w-md">
            <div className="mx-auto w-16 h-16 bg-red-100 dark:bg-red-900/20 rounded-full flex items-center justify-center mb-4">
              <span className="text-3xl">⚠️</span>
            </div>
            <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100 mb-2">
              Something went wrong
            </h2>
            <p className="text-gray-600 dark:text-gray-400 mb-4">
              An unexpected error occurred. Please try refreshing the page.
            </p>
            {this.state.error && (
              <details className="mt-4 text-left">
                <summary className="cursor-pointer text-sm text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300">
                  Error details
                </summary>
                <pre className="mt-2 p-3 bg-gray-100 dark:bg-gray-800 rounded text-xs overflow-auto max-h-48">
                  {this.state.error.toString()}
                </pre>
              </details>
            )}
            <button
              onClick={() => window.location.reload()}
              className="mt-4 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 transition-colors"
            >
              Refresh Page
            </button>
          </div>
        </div>
      )
    }

    return this.props.children
  }
}

/**
 * Convenience component for wrapping page-level components
 */
export function PageErrorBoundary({ children }: { children: ReactNode }) {
  return (
    <ErrorBoundary
      onError={(error, errorInfo) => {
        // In production, you might want to send this to an error tracking service
        // Error details are already displayed in the UI, so we don't need console logging
        void error
        void errorInfo
      }}
    >
      {children}
    </ErrorBoundary>
  )
}

/**
 * Section-specific error boundary fallback component
 * Provides a more localized error UI without requiring a full page refresh
 */
interface SectionErrorFallbackProps {
  title: string
  description: string
  icon?: string
  error?: Error | null
  onReset?: () => void
  resetLabel?: string
}

function SectionErrorFallback({
  title,
  description,
  icon = '⚠️',
  error,
  onReset,
  resetLabel = 'Try Again',
}: SectionErrorFallbackProps) {
  return (
    <div className="flex items-center justify-center min-h-[300px] p-6">
      <div className="text-center max-w-md">
        <div className="mx-auto w-16 h-16 bg-red-100 dark:bg-red-900/20 rounded-full flex items-center justify-center mb-4">
          <span className="text-3xl">{icon}</span>
        </div>
        <h2 className="text-xl font-bold text-gray-900 dark:text-gray-100 mb-2">{title}</h2>
        <p className="text-gray-600 dark:text-gray-400 mb-4">{description}</p>
        {error && (
          <details className="mt-4 text-left">
            <summary className="cursor-pointer text-sm text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300">
              Error details
            </summary>
            <pre className="mt-2 p-3 bg-gray-100 dark:bg-gray-800 rounded text-xs overflow-auto max-h-48">
              {error.toString()}
              {error.stack && `\n\n${error.stack}`}
            </pre>
          </details>
        )}
        {onReset && (
          <button
            onClick={onReset}
            className="mt-4 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 transition-colors"
          >
            {resetLabel}
          </button>
        )}
      </div>
    </div>
  )
}

/**
 * Error boundary specifically for the Explore section
 * Provides section-specific error handling and recovery
 */
export class ExploreErrorBoundary extends Component<
  { children: ReactNode },
  ErrorBoundaryState
> {
  constructor(props: { children: ReactNode }) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    console.error('Explore section error:', error, errorInfo)
  }

  resetErrorBoundary = () => {
    this.setState({ hasError: false, error: null })
  }

  render() {
    if (this.state.hasError) {
      return (
        <SectionErrorFallback
          title="Error in Explore Section"
          description="Something went wrong while exploring your cluster data. You can try reloading this section or switch to another tab."
          icon="🔍"
          error={this.state.error}
          onReset={this.resetErrorBoundary}
          resetLabel="Reload Explore"
        />
      )
    }

    return this.props.children
  }
}

/**
 * Error boundary specifically for the Migration Assets section
 * Provides section-specific error handling and recovery
 */
export class MigrationErrorBoundary extends Component<
  { children: ReactNode },
  ErrorBoundaryState
> {
  constructor(props: { children: ReactNode }) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    console.error('Migration section error:', error, errorInfo)
  }

  resetErrorBoundary = () => {
    this.setState({ hasError: false, error: null })
  }

  render() {
    if (this.state.hasError) {
      return (
        <SectionErrorFallback
          title="Error in Migration Section"
          description="Something went wrong while managing migration assets. You can try reloading this section or switch to another tab."
          icon="🚀"
          error={this.state.error}
          onReset={this.resetErrorBoundary}
          resetLabel="Reload Migration"
        />
      )
    }

    return this.props.children
  }
}

/**
 * Error boundary specifically for the TCO Inputs section
 * Provides section-specific error handling and recovery
 */
export class TCOErrorBoundary extends Component<{ children: ReactNode }, ErrorBoundaryState> {
  constructor(props: { children: ReactNode }) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    console.error('TCO section error:', error, errorInfo)
  }

  resetErrorBoundary = () => {
    this.setState({ hasError: false, error: null })
  }

  render() {
    if (this.state.hasError) {
      return (
        <SectionErrorFallback
          title="Error in TCO Inputs Section"
          description="Something went wrong while managing TCO inputs. You can try reloading this section or switch to another tab."
          icon="💰"
          error={this.state.error}
          onReset={this.resetErrorBoundary}
          resetLabel="Reload TCO Inputs"
        />
      )
    }

    return this.props.children
  }
}

