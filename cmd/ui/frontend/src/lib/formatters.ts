/**
 * Date formatting utilities
 * Centralized date formatting functions used across the application
 */

/**
 * Formats a date string into a readable date-time format
 * @param dateString - ISO date string or date string
 * @returns Formatted date string (e.g., "Jan 15, 2024, 10:30 AM")
 */
export function formatDate(dateString: string): string {
  return new Date(dateString).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/**
 * Formats a date string into a short date format (month and day only)
 * Used primarily in chart labels
 * @param dateString - ISO date string or date string
 * @returns Short formatted date (e.g., "Jan 15")
 */
export function formatDateShort(dateString: string): string {
  return new Date(dateString).toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
  })
}

/**
 * Formats a date string into date-time format with full details
 * Alias for formatDate for clarity when date-time is explicitly needed
 * @param dateString - ISO date string or date string
 * @returns Formatted date-time string
 */
export function formatDateTime(dateString: string): string {
  return formatDate(dateString)
}

/**
 * Formats a Date object into a readable date-time format
 * @param date - Date object
 * @returns Formatted date string
 */
export function formatDateObject(date: Date): string {
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/**
 * Status badge configuration helper
 * Returns className and display text for status badges
 * @param enabled - Whether the status is enabled
 * @returns Object with className and display text
 */
export function getStatusBadgeConfig(enabled: boolean): {
  className: string
  text: string
} {
  return {
    className: enabled
      ? 'text-green-600 dark:text-green-400'
      : 'text-red-600 dark:text-red-400',
    text: enabled ? '✓' : '✗',
  }
}


