import type { ReactElement } from 'react'
import { useTranslation } from '@speed/i18n'
import { DEMO_NAMESPACE } from './resources.js'

export interface DemoNavLabelProps {
  readonly labelKey: string
}

export function DemoNavLabel({ labelKey }: DemoNavLabelProps): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return <>{t(labelKey)}</>
}
