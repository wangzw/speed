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

export interface NotificationInbox {
  readonly messages: NonNullable<NotificationInboxListMessages200['messages']>
  readonly total: number
}

function useActingTenantId(): string | null {
  return useCurrentTenant()?.tenantId ?? null
}

export interface NotificationMessageListing {
  readonly group?: string
  readonly limit?: number
  readonly offset?: number
}

export function useNotificationMessages(listing?: NotificationMessageListing) {
  const group = listing?.group
  const limit = listing?.limit
  const offset = listing?.offset
  const params = useMemo(() => {
    const next: { group?: string; limit?: number; offset?: number } = {}
    if (group !== undefined && group !== '') {
      next.group = group
    }
    if (limit !== undefined) {
      next.limit = limit
    }
    if (offset !== undefined) {
      next.offset = offset
    }
    return Object.keys(next).length === 0 ? undefined : next
  }, [group, limit, offset])
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

export function useNotificationUnreadCount(enabled = true) {
  const bareKey = useMemo(() => getNotificationInboxGetUnreadCountQueryKey(), [])
  const { tenantId, queryKey } = useTenantQueryKey(bareKey)
  return useNotificationInboxGetUnreadCount<number>({
    query: {
      queryKey,
      enabled: enabled && tenantId !== null,
      select: (data) => data?.unread ?? 0,
    },
  })
}

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

function useInvalidateNotifications(): () => void {
  const tenantId = useActingTenantId()
  const queryClient = useQueryClient()
  return useCallback(() => {
    if (tenantId === null) {
      return
    }
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

export function useMarkNotificationRead() {
  const invalidate = useInvalidateNotifications()
  return useNotificationInboxMarkMessageRead({
    mutation: { onSuccess: () => invalidate() },
  })
}

export function useMarkAllNotificationsRead() {
  const invalidate = useInvalidateNotifications()
  return useNotificationInboxMarkAllMessagesRead({
    mutation: { onSuccess: () => invalidate() },
  })
}

export function useSetNotificationPreference() {
  const invalidate = useInvalidateNotifications()
  return useNotificationPreferencesUpdatePreference({
    mutation: { onSuccess: () => invalidate() },
  })
}
