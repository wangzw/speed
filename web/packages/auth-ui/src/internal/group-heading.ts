/**
 * The group heading variant of the assembled sign-in surface.
 *
 * The screen renders its channel classes in a host-chosen order. A
 * divider that heads a group below another one says "or continue with
 * ..."; the first group heads the surface itself, so the "or" would refer
 * to nothing above it. The screen tells its sections which variant to
 * render through this internal context -- the standalone sections keep
 * their default "or" wording, because a host mounting one alone is
 * composing it beneath its own preceding content.
 */

import { createContext, useContext } from 'react'

/** Whether a group's divider heading is the surface's first. */
export type GroupHeadingVariant = 'or' | 'plain'

const GroupHeadingContext = createContext<GroupHeadingVariant>('or')

export const GroupHeadingProvider = GroupHeadingContext.Provider

/** The divider variant the enclosing sign-in screen asks for. */
export function useGroupHeadingVariant(): GroupHeadingVariant {
  return useContext(GroupHeadingContext)
}
