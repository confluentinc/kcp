import RegionCosts from './RegionCosts'

export default function RegionReport({ region }: any) {
  return (
    <div className="max-w-7xl mx-auto space-y-6">
      {/* Region Header */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold text-gray-900 dark:text-gray-100">
              Region:&nbsp;{region.name}
            </h1>
          </div>
        </div>
      </div>

      {/* Region Costs Component */}
      <RegionCosts
        region={region}
        isActive={true}
      />
    </div>
  )
}
