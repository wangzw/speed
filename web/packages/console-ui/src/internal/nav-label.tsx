/**
 * A navigation label bound to a console-ui namespace key, resolved when
 * the label renders.
 *
 * It lets the console's module-level declarations and its group table
 * stay plain values -- the shape the registration seam wants -- while
 * their labels translate reactively with the host's language. Shared by
 * the page declarations (routes.tsx) and the group table (nav-groups.tsx).
 */

import { useConsoleUiTranslation } from './translation.js'

export function ConsoleNavLabel({ labelKey }: { readonly labelKey: string }) {
  const { t } = useConsoleUiTranslation()
  return <>{t(labelKey)}</>
}
