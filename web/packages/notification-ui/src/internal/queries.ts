/**
 * The notification surface's reads and writes, one hook each over the
 * generated react-query hooks of @speed/api-sdk.
 *
 * Every read is keyed under the acting tenant through auth-core's
 * tenantQueryKey (frontend standards: a query key whose outermost
 * segment is the tenant, so a tenant switch can never serve the previous
 * tenant's rows) and enabled only while a tenant is known, failing
 * closed rather than inventing one. The generated constructors supply the
 * key's own segments, so the shape a write invalidates by cannot drift
 * from the shape a read cached under.
 *
 * The writes -- mark one read, mark every unread read, set one type's
 * channel -- invalidate the tenant's message list, unread count and
 * preference matrix on success, so the surfaces follow the server's
 * answer without a hand-maintained query-key copy.
 */

import { useCallback, useMemo } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  getNotificationInboxGetUnreadCountQueryKey,
  getNotificationInboxListMessagesQueryKey,
  getNotificationPreferencesListPreferencesQueryKey,
  getNotificationPreferencesListTypesQueryKey,
  useNotificationInboxGetUnreadCount,
  useNotificationInboxListMessages,
  useNotificationInboxMarkAllMessagesRead,
  useNotificationInboxMarkMessageRead,
  useNotificationPreferencesListPreferences,
  useNotificationPreferencesListTypes,
  useNotificationPreferencesUpdatePreference,
} from '@speed/api-sdk'
import type {
  NotificationInboxListMessages200,
  NotificationPreference,
  NotificationType,
} from '@speed/api-sdk'
import { tenantQueryKey, useCurrentTenant, useTenantQueryKey } from '@speed/auth-core'

/** A page of the caller's messages, reduced so an absent body reads as an
 * empty page rather than undefined. */
export interface NotificationInbox {
  readonly messages: NonNullable<NotificationInboxListMessages200['messages']>
  readonly total: number
}

/** The current tenant id, or null when no tenant route/provider is set. */
function useActingTenantId(): string | null {
  return useCurrentTenant()?.tenantId ?? null
}

/** The caller's messages, optionally filtered by group (GET
 * /notifications/messages). */
export function useNotificationMessages(group?: string) {
  const params = useMemo(
    () => (group === undefined || group === '' ? undefined : { group }),
    [group],
  )
  const bareKey = useMemo(
    () => getNotificationInboxListMessagesQueryKey(params),
    [params],
  )
  const { tenantId, queryKey } = useTenantQueryKey(bareKey)
  return useNotificationInboxListMessages<NotificationInbox>(params, {
    query: {
      queryKey,
      enabled: tenantId !== null,
      select: (data) => ({
        messages: data?.messages ?? [],
        total: data?.total ?? 0,
      }),
    },
  })
}

/** The number of the caller's unread, unexpired messages (GET
 * /notifications/messages/unread-count); undefined until it settles. */
export function useNotificationUnreadCount() {
  const bareKey = useMemo(() => getNotificationInboxGetUnreadCountQueryKey(), [])
  const { tenantId, queryKey } = useTenantQueryKey(bareKey)
  return useNotificationInboxGetUnreadCount<number>({
    query: {
      queryKey,
      enabled: tenantId !== null,
      select: (data) => data?.unread ?? 0,
    },
  })
}

/** The caller's preference matrix, one row per type in the taxonomy (GET
 * /notifications/preferences). */
export function useNotificationPreferences() {
  const bareKey = useMemo(
    () => getNotificationPreferencesListPreferencesQueryKey(),
    [],
  )
  const { tenantId, queryKey } = useTenantQueryKey(bareKey)
  return useNotificationPreferencesListPreferences<readonly NotificationPreference[]>(
    {
      query: {
        queryKey,
        enabled: tenantId !== null,
        select: (data) => data?.preferences ?? [],
      },
    },
  )
}

/** The preference matrix's type taxonomy, one entry per declared type
 * (GET /notifications/types): the per-type `unsubscribable` flag the
 * matrix disables a row by and the default channels the matrix hints. */
export function useNotificationTypes() {
  const bareKey = useMemo(() => getNotificationPreferencesListTypesQueryKey(), [])
  const { tenantId, queryKey } = useTenantQueryKey(bareKey)
  return useNotificationPreferencesListTypes<readonly NotificationType[]>({
    query: {
      queryKey,
      enabled: tenantId !== null,
      select: (data) => data?.types ?? [],
    },
  })
}

/** Invalidates the acting tenant's message list, unread count and
 * preference matrix: the write path's stale-marking, built from the
 * generated key constructors so it can never drift from the read keys. */
function useInvalidateNotifications(): () => void {
  const tenantId = useActingTenantId()
  const queryClient = useQueryClient()
  return useCallback(() => {
    if (tenantId === null) {
      return
    }
    // The bare constructors are the reads' own key tails; prefixing them
    // with the tenant matches every cached entry (the list's prefix also
    // reaches a list cached under a group filter).
    for (const tail of [
      getNotificationInboxListMessagesQueryKey(),
      getNotificationInboxGetUnreadCountQueryKey(),
      getNotificationPreferencesListPreferencesQueryKey(),
    ]) {
      void queryClient.invalidateQueries({
        queryKey: tenantQueryKey(tenantId, ...tail),
      })
    }
  }, [queryClient, tenantId])
}

/** Marks one message read (POST /notifications/messages/:id/read) and
 * refreshes the affected reads. */
export function useMarkNotificationRead() {
  const invalidate = useInvalidateNotifications()
  return useNotificationInboxMarkMessageRead({
    mutation: { onSuccess: () => invalidate() },
  })
}

/** Marks every unread message read (POST
 * /notifications/messages/read-all) and refreshes the affected reads. */
export function useMarkAllNotificationsRead() {
  const invalidate = useInvalidateNotifications()
  return useNotificationInboxMarkAllMessagesRead({
    mutation: { onSuccess: () => invalidate() },
  })
}

/** Turns one channel on or off for one type (PUT
 * /notifications/preferences/:typeKey/:channel) and refreshes the matrix. */
export function useSetNotificationPreference() {
  const invalidate = useInvalidateNotifications()
  return useNotificationPreferencesUpdatePreference({
    mutation: { onSuccess: () => invalidate() },
  })
}
