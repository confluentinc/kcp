/**
 * Formats a cost type string into a human-readable label
 * Example: "unblended_cost" -> "Unblended Cost"
 */
export const formatCostTypeLabel = (costType: string): string => {
  return costType.replace(/_/g, ' ').replace(/\b\w/g, (l) => l.toUpperCase())
}

