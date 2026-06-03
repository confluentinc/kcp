import { useEffect, useState } from 'react'
import { apiClient } from '@/services/apiClient'

/**
 * GovBanner renders a prominent banner when the backing kcp binary is the gov
 * (kcp-lite) edition. It fetches the edition once on mount and fails safe to
 * hidden: any error, or a non-gov edition, renders nothing — so a prod build or
 * a transient fetch failure never shows a misleading banner.
 */
export const GovBanner = () => {
  const [isGov, setIsGov] = useState(false)

  useEffect(() => {
    let cancelled = false
    apiClient.edition
      .getEdition()
      .then((res) => {
        if (!cancelled) setIsGov(res.mode === 'gov')
      })
      .catch(() => {
        // Fail safe: leave the banner hidden on any error.
      })
    return () => {
      cancelled = true
    }
  }, [])

  if (!isGov) return null

  return (
    <div
      role="status"
      data-testid="gov-banner"
      className="w-full bg-red-700 text-white text-center text-sm font-bold tracking-wider py-1.5 px-4 shadow-md"
    >
      {/* Keep this list in sync with cmd/create_asset/register_full.go (the
          gov-excluded command set). */}
      GOV MODE — kcp-lite (target-infra, migration-infra, and connector migration are not available)
    </div>
  )
}
