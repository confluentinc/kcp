/**
 * Date formatting utilities
 * Centralized date formatting functions used across the application
 */

/**
 * Formats a date string into a readable date-time format
 * @param dateString - ISO date string or date string
 * @returns Formatted date string (e.g., "Jan 15, 2024, 10:30 AM")
 */
export const formatDate = (dateString: string): string => {
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
export const formatDateShort = (dateString: string): string => {
  return new Date(dateString).toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
  })
}
