import { useTranslation } from '@speed/i18n'
import { TENANCY_UI_NAMESPACE } from '../resources.js'

export function useTenancyUiTranslation() {
  return useTranslation(TENANCY_UI_NAMESPACE)
}
