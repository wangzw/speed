/** A navigation label bound to an admin-ui namespace key, resolved when it renders. */

import { useAdminUiTranslation } from './translation.js'

export function AdminNavLabel({ labelKey }: { readonly labelKey: string }) {
  const { t } = useAdminUiTranslation()
  return <>{t(labelKey)}</>
}
