import { useTranslation } from '@speed/i18n'
import { LAYOUT_KIT_NAMESPACE } from '../resources.js'

export function useLayoutKitTranslation() {
  return useTranslation(LAYOUT_KIT_NAMESPACE)
}
