/** The divider-heading variant the assembled sign-in surface asks for. */

import { createContext, useContext } from 'react'

export type GroupHeadingVariant = 'or' | 'plain'

const GroupHeadingContext = createContext<GroupHeadingVariant>('or')

export const GroupHeadingProvider = GroupHeadingContext.Provider

export function useGroupHeadingVariant(): GroupHeadingVariant {
  return useContext(GroupHeadingContext)
}
