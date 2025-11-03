import { Button } from '@/components/common/ui/button'
import Form from '@rjsf/shadcn'
import validator from '@rjsf/validator-ajv8'
import type { RJSFSchema, UiSchema } from '@rjsf/utils'
import type { WizardStep, WizardFormData } from '../types'

interface WizardStepFormProps {
  step: WizardStep
  formData: WizardFormData
  onSubmit: (formData: WizardFormData) => void
  onBack: () => void
  onClose?: () => void
  canGoBack: boolean
  isLoading?: boolean
}

export const WizardStepForm = ({
  step,
  formData,
  onSubmit,
  onBack,
  onClose,
  canGoBack,
  isLoading = false,
}: WizardStepFormProps) => {
  const schema = step.schema as RJSFSchema
  const uiSchema = step.uiSchema as UiSchema

  if (!schema) {
    return <div className="text-gray-900 dark:text-gray-100">Invalid step configuration</div>
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{step.title}</h2>
        {step.description && (
          <p className="text-gray-600 dark:text-gray-400 mt-2">{step.description}</p>
        )}
      </div>

      <Form
        schema={schema}
        uiSchema={uiSchema}
        formData={formData}
        onSubmit={({ formData }) => onSubmit(formData)}
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
          {canGoBack && (
            <Button
              type="button"
              onClick={onBack}
              variant="outline"
              disabled={isLoading}
            >
              Back
            </Button>
          )}
          {onClose && (
            <Button
              type="button"
              onClick={onClose}
              variant="outline"
              disabled={isLoading}
            >
              Close
            </Button>
          )}
          <Button
            type="submit"
            className="flex-1"
            disabled={isLoading}
          >
            {isLoading ? 'Generating...' : 'Next'}
          </Button>
        </div>
      </Form>
    </div>
  )
}
