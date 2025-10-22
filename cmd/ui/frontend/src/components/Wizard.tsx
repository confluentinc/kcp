import { useMemo, useState } from 'react'
import { createMachine, assign } from 'xstate'
import { useMachine } from '@xstate/react'
import Form from '@rjsf/shadcn'
import type { RJSFSchema, UiSchema } from '@rjsf/utils'
import { Button } from '@/components/ui/button'
import wizardConfig from './confluent-cloud-wizard-config.json'
import validator from '@rjsf/validator-ajv8'

interface WizardStateMeta {
  title?: string
  description?: string
  schema?: RJSFSchema
  uiSchema?: UiSchema
  type?: string
  summaryFields?: string[]
  message?: string
  apiEndpoint?: string
}

interface WizardConfig {
  id: string
  title: string
  description: string
  xstateMachine: any
  guards: Record<string, string>
  actions: Record<string, string>
}

interface TerraformFiles {
  main_tf: string
  providers_tf: string
  variables_tf: string
}

interface WizardProps {
  config?: WizardConfig
}

export default function Wizard({ config: configProp }: WizardProps = {}) {
  const config = (configProp || wizardConfig) as unknown as WizardConfig

  // Store form data in component state as a backup
  const [componentFormData, setComponentFormData] = useState<Record<string, any>>({})

  // Store terraform files from API response
  const [terraformFiles, setTerraformFiles] = useState<TerraformFiles | null>(null)
  const [isLoading, setIsLoading] = useState(false)

  // Create XState machine from JSON configuration
  const wizardMachine = useMemo(() => {
    const machineConfig = config.xstateMachine

    // Create guards from JSON strings - completely dynamic
    const guards = Object.entries(config.guards).reduce((acc, [key, guardFn]) => {
      try {
        // Create a function that has access to context, event, and meta
        // The guardFn string should be a valid JavaScript expression
        // XState v5 guards receive ({ context, event }, params)
        acc[key] = ({ context, event }: any, params?: any) => {
          try {
            // Create a function with the proper XState v5 signature
            const guardFunction = new Function('context', 'event', 'params', `return ${guardFn}`)
            const result = guardFunction(context, event, params)
            return result
          } catch (error) {
            console.error(`Error evaluating guard ${key}:`, error)
            return false
          }
        }
      } catch (error) {
        console.error(`Error creating guard ${key}:`, error)
        // Fallback guard that always returns false
        acc[key] = () => false
      }
      return acc
    }, {} as Record<string, any>)

    // Create actions from JSON strings - completely dynamic
    const actions = Object.entries(config.actions).reduce((acc, [key, actionFn]) => {
      try {
        if (key === 'save_step_data') {
          // Special handling for saveStepData since it needs assign
          // XState v5 uses an object parameter { context, event } not separate parameters
          acc[key] = assign(({ context, event }: any) => {
            // Use stepId from event if available, otherwise fall back to context.currentStep
            const currentStateId = event?.stepId || context.currentStep || 'unknown'

            // Try to get event data from multiple sources
            let eventData = {}
            if (event && event.data) {
              eventData = event.data
            } else if (event && event.formData) {
              eventData = event.formData
            } else if (event && typeof event === 'object' && event.type) {
              // Extract data properties from the event object
              // eslint-disable-next-line @typescript-eslint/no-unused-vars
              const { type, stepId, ...restData } = event as any
              eventData = restData.data || restData.formData || restData
            } else if (event && typeof event === 'object') {
              // Sometimes the entire event is the data
              eventData = event
            }

            return {
              stepData: {
                ...context.stepData,
                [currentStateId]: eventData,
              },
              allData: {
                ...context.allData,
                [currentStateId]: eventData,
              },
              previousStep: currentStateId,
            }
          })
        } else {
          // For other actions, evaluate the JSON string as JavaScript
          acc[key] = new Function('context', 'event', `return ${actionFn}`)
        }
      } catch (error) {
        console.error(`Error creating action ${key}:`, error)
        // Fallback action that does nothing
        acc[key] = () => ({})
      }
      return acc
    }, {} as Record<string, any>)

    // Modify machine config to set currentStep for each state
    const modifiedMachineConfig = {
      ...machineConfig,
      states: Object.entries(machineConfig.states).reduce((acc, [stateId, stateConfig]) => {
        acc[stateId] = {
          ...(stateConfig as object),
          entry: assign({
            currentStep: stateId,
          }),
        }
        return acc
      }, {} as any),
    }

    return createMachine(modifiedMachineConfig, {
      guards,
      actions,
    })
  }, [config])

  const [state, send] = useMachine(wizardMachine)

  const currentStateId = state.value as string

  // Get current state meta - try different access patterns
  let currentStateMeta: WizardStateMeta = {}

  // Try direct access to state definition
  if (state.machine.config.states && state.machine.config.states[currentStateId]) {
    currentStateMeta = (state.machine.config.states[currentStateId].meta || {}) as WizardStateMeta
  }

  const handleFormSubmit = (formData: any) => {
    const eventType = currentStateMeta.type === 'review' ? 'SUBMIT' : 'NEXT'
    const currentStateId = state.value as string

    // Store the form data in component state as backup
    const updatedComponentData = {
      ...componentFormData,
      [currentStateId]: formData,
    }
    setComponentFormData(updatedComponentData)

    // Send the event with both the form data and the current step ID
    send({
      type: eventType,
      data: formData,
      stepId: currentStateId,
      formData: formData, // Try multiple ways to pass the data
    })
  }

  const handleBack = () => {
    send({
      type: 'BACK',
    })
  }

  // Helper function to flatten nested step data
  const getFlattenedData = () => {
    const flattened: Record<string, any> = {}

    // Try allData first
    Object.entries(state.context.allData || {}).forEach(([, stepData]: [string, any]) => {
      if (stepData && typeof stepData === 'object') {
        Object.assign(flattened, stepData)
      }
    })

    // Fallback to stepData if allData is empty
    if (Object.keys(flattened).length === 0) {
      Object.entries(state.context.stepData || {}).forEach(([, stepData]: [string, any]) => {
        if (stepData && typeof stepData === 'object') {
          Object.assign(flattened, stepData)
        }
      })
    }

    // Final fallback to component form data
    if (Object.keys(flattened).length === 0) {
      Object.entries(componentFormData || {}).forEach(([, stepData]: [string, any]) => {
        if (stepData && typeof stepData === 'object') {
          Object.assign(flattened, stepData)
        }
      })
    }

    return flattened
  }

  const handleSubmitToAPI = async () => {
    try {
      setIsLoading(true)
      
      // Get all the wizard data and send it to the API
      const wizardData = getFlattenedData()
      
      const response = await fetch('/assets', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(wizardData),
      })

      if (response.ok) {
        const files = (await response.json()) as TerraformFiles
        setTerraformFiles(files)
      } else {
        throw new Error('Failed to generate Terraform files')
      }
    } catch (error) {
      console.error('API Error:', error)
      alert('Error generating Terraform files')
    } finally {
      setIsLoading(false)
    }
  }

  // Handle final state
  if (state.matches('complete')) {
    return (
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        <div className="space-y-4">
          <div className="text-center">
            <h2 className="text-2xl font-bold text-green-600 dark:text-green-400">
              {currentStateMeta.title || 'Complete'}
            </h2>
            <p className="text-gray-600 dark:text-gray-400">
              {currentStateMeta.message || 'Configuration complete'}
            </p>
          </div>

          {!terraformFiles && (
            <div className="bg-gray-50 dark:bg-gray-700 p-4 rounded-lg">
              <h3 className="font-semibold mb-2 text-gray-900 dark:text-gray-100">
                Configuration Summary:
              </h3>
              <pre className="text-sm text-left overflow-auto text-gray-800 dark:text-gray-200">
                {JSON.stringify(getFlattenedData(), null, 2)}
              </pre>
            </div>
          )}

          {terraformFiles && (
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
          )}

          <Button
            onClick={handleSubmitToAPI}
            className="w-full"
            disabled={isLoading}
          >
            {isLoading
              ? 'Generating...'
              : terraformFiles
              ? 'Regenerate Terraform Files'
              : 'Generate Terraform Files'}
          </Button>
        </div>
      </div>
    )
  }

  // Handle review state
  if (currentStateMeta.type === 'review') {
    // Get flattened data using helper function
    const flattenedData = getFlattenedData()

    // Build summary from configured fields
    const summaryFields = currentStateMeta.summaryFields || []

    // Get all schema definitions to identify boolean fields
    const allSchemas = Object.values(config.xstateMachine.states)
      .map((state: any) => state.meta?.schema)
      .filter(Boolean)
    const booleanFields = new Set<string>()

    allSchemas.forEach((schema: any) => {
      if (schema.properties) {
        Object.entries(schema.properties).forEach(([fieldName, fieldDef]: [string, any]) => {
          if (fieldDef.type === 'boolean') {
            booleanFields.add(fieldName)
          }
        })
      }
    })

    const summaryData = summaryFields.reduce((acc: Record<string, any>, field: string) => {
      const value = flattenedData[field]

      // Always include boolean fields, even if false or undefined
      if (booleanFields.has(field)) {
        acc[field] = value === true ? true : false
      }
      // Include other fields that have actual values
      else if (value !== undefined && value !== null && value !== '') {
        acc[field] = value
      }

      return acc
    }, {})

    return (
      <div className="max-w-2xl mx-auto p-6 space-y-6">
        <div className="space-y-4">
          <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">
            {currentStateMeta.title || 'Review'}
          </h2>
          <p className="text-gray-600 dark:text-gray-400">
            {currentStateMeta.description || 'Review your configuration'}
          </p>

          <div className="bg-gray-50 dark:bg-gray-700 p-4 rounded-lg space-y-2">
            <h3 className="font-semibold text-gray-900 dark:text-gray-100">
              Configuration Summary:
            </h3>
            {Object.entries(summaryData).map(([key, value]) => (
              <div
                key={key}
                className="flex justify-between"
              >
                <span className="font-medium capitalize text-gray-900 dark:text-gray-100">
                  {key.replace(/([A-Z])/g, ' $1')}:
                </span>
                <span className="text-gray-700 dark:text-gray-300">{String(value)}</span>
              </div>
            ))}
          </div>

          <div className="flex gap-4">
            <Button
              onClick={handleBack}
              variant="outline"
            >
              Back
            </Button>
            <Button
              onClick={() => handleFormSubmit({})}
              className="flex-1"
            >
              Submit Configuration
            </Button>
          </div>
        </div>
      </div>
    )
  }

  // Handle regular form states
  const schema = currentStateMeta.schema as RJSFSchema
  const uiSchema = currentStateMeta.uiSchema as UiSchema
  const title = currentStateMeta.title || 'Step'
  const description = currentStateMeta.description || ''

  if (!schema) {
    return <div className="text-gray-900 dark:text-gray-100">Invalid step configuration</div>
  }

  // Calculate step progress (approximate)
  const allStates = Object.keys(config.xstateMachine.states)
  const nonFinalStates = allStates.filter((s) => config.xstateMachine.states[s].type !== 'final')
  const currentIndex = nonFinalStates.indexOf(currentStateId)
  const progress = currentIndex >= 0 ? ((currentIndex + 1) / nonFinalStates.length) * 100 : 0

  return (
    <div className="max-w-2xl mx-auto p-6 space-y-6">
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{title}</h2>
            <p className="text-gray-600 dark:text-gray-400">{description}</p>
          </div>
          <div className="text-sm text-gray-500 dark:text-gray-400">
            Step {currentIndex + 1} of {nonFinalStates.length}
          </div>
        </div>

        <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-2">
          <div
            className="bg-blue-600 dark:bg-blue-500 h-2 rounded-full transition-all duration-300"
            style={{ width: `${progress}%` }}
          />
        </div>

        <Form
          schema={schema}
          uiSchema={uiSchema}
          formData={state.context.stepData?.[currentStateId] || {}}
          onSubmit={({ formData }) => handleFormSubmit(formData)}
          validator={validator}
          showErrorList="top"
          noHtml5Validate={true}
          omitExtraData={false}
          liveOmit={false}
          experimental_defaultFormStateBehavior={{
            emptyObjectFields: 'populateRequiredDefaults',
          }}
        >
          <div className="flex gap-4 mt-6">
            <Button
              type="button"
              onClick={handleBack}
              variant="outline"
              disabled={currentIndex === 0}
            >
              Back
            </Button>
            <Button
              type="submit"
              className="flex-1"
            >
              {currentStateMeta.type === 'review' ? 'Submit' : 'Next'}
            </Button>
          </div>
        </Form>
      </div>
    </div>
  )
}
