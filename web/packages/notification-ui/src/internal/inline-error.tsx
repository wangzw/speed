import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useNotificationUiErrorText } from './error-text.js'

export interface InlineErrorProps {
  readonly code: string | null
}

export function InlineError({ code }: InlineErrorProps) {
  const resolve = useNotificationUiErrorText()
  return <SharedInlineError code={code} resolve={resolve} />
}
