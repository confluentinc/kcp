import type { TerraformFiles } from '../types'

interface TerraformDisplayProps {
  terraformFiles: TerraformFiles
}

export function TerraformDisplay({ terraformFiles }: TerraformDisplayProps) {
  return (
    <div className="space-y-4">
      <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
        Generated Terraform Files
      </h3>

      <div className="space-y-3">
        <div>
          <label className="block text-sm font-medium text-gray-900 dark:text-gray-100 mb-2">
            main.tf
          </label>
          <textarea
            readOnly
            value={terraformFiles.main_tf}
            className="w-full h-64 p-3 font-mono text-sm bg-white dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded-md text-gray-900 dark:text-gray-100"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-900 dark:text-gray-100 mb-2">
            providers.tf
          </label>
          <textarea
            readOnly
            value={terraformFiles.providers_tf}
            className="w-full h-64 p-3 font-mono text-sm bg-white dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded-md text-gray-900 dark:text-gray-100"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-900 dark:text-gray-100 mb-2">
            variables.tf
          </label>
          <textarea
            readOnly
            value={terraformFiles.variables_tf}
            className="w-full h-64 p-3 font-mono text-sm bg-white dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded-md text-gray-900 dark:text-gray-100"
          />
        </div>
      </div>
    </div>
  )
}
