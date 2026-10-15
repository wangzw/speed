/**
 * A navigation label bound to a demo-namespace key, resolved when it renders.
 *
 * A route declaration's label is a React element, so a declaration stays a
 * plain module-level value (the shape route-kit's registration surface wants)
 * while its label still translates reactively with the host's active language.
 */

import type { ReactElement } from 'react'
import { useTranslation } from '@speed/i18n'
import { DEMO_NAMESPACE } from './resources.js'

export interface DemoNavLabelProps {
  /** The demo-namespace key the label resolves from. */
  readonly labelKey: string
}

export function DemoNavLabel({ labelKey }: DemoNavLabelProps): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return <>{t(labelKey)}</>
}
