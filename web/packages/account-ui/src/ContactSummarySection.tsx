/**
 * ContactSummarySection: the read-only contact summary of the account page.
 *
 * A reader opening account settings asks one question first: which email
 * addresses and phone numbers is this account bound to, and which of them
 * are verified? The verification section answers only for the pending
 * address and the identifiers section groups records by the sign-in and
 * contact axes, so neither states the bare inventory. This section does:
 * every email identifier and, when the deployment serves the phone
 * capability, every phone identifier, each rendered as its value with the
 * same two-value verification badge the identifiers section uses
 * (identifiers.verified / identifiers.unverified, read off the server's
 * `proven` flag). A kind with no identifier renders the section's own "not
 * added yet" note instead of disappearing, so the reader still sees that
 * the kind exists and is unbound.
 *
 * The identifiers are the account's own, read off the profile (GET
 * /api/v1/auth/me, authn.profile.get) through the package's share of that
 * read -- the same hook every sibling section uses, so the page issues one
 * request and every section converges on one answer. A profile answer that
 * omitted the identifiers key is the read's error variant, never a claim
 * that the account holds none, exactly as the identifiers section treats it.
 *
 * The section is deliberately read-only: it renders no buttons and holds no
 * forms. Proving an address stays with the verification card, and adding,
 * removing or reassigning an identifier stays with the identifiers section
 * below; this surface only reports what the account currently carries. Its
 * prop says whether the deployment serves phones at all, so a deployment
 * without the phone capability renders no phone row -- there is no number
 * it could hold.
 */

import { useId } from 'react'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import type { AuthnProfileIdentifier } from '@speed/api-sdk'
import { ListSkeleton, SectionCard } from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useProfileQuery } from './internal/queries.js'
import { useAccountUiTranslation } from './internal/translation.js'

export interface ContactSummarySectionProps {
  /**
   * Whether the deployment serves the phone capability. False renders no
   * phone rows at all -- the deployment holds no phone number for the
   * account to show. Defaults to true.
   */
  readonly phoneServed?: boolean
}

/** One contact kind the section lists, and the identifier kind it keeps. */
type ContactKind = 'email' | 'phone'

/** The pending-state placeholder, read aloud as one loading announcement. */
function ContactListSkeleton({ label }: { readonly label: string }) {
  return (
    <ListSkeleton
      label={label}
      rows={[
        <>
          <Skeleton variant="text" width="46%" />
          <Skeleton variant="text" width="20%" />
        </>,
        <>
          <Skeleton variant="text" width="34%" />
          <Skeleton variant="text" width="20%" />
        </>,
      ]}
    />
  )
}

export function ContactSummarySection({
  phoneServed = true,
}: ContactSummarySectionProps) {
  const { t } = useAccountUiTranslation()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const groupIdBase = useId()

  const pending = isPending && data === undefined
  // A settled answer that omitted the identifiers key stays `undefined`
  // (the read's error variant); an answer that genuinely listed none is an
  // empty list and every kind renders its "not added yet" note.
  const identifiers =
    !pending && data !== undefined ? data.identifiers : undefined

  const kinds: readonly ContactKind[] = phoneServed
    ? ['email', 'phone']
    : ['email']

  /**
   * One contact row: the value and its verification badge, in the same
   * row markup the identifiers section uses, so the two surfaces read
   * alike. No per-record action renders here -- the section is read-only.
   */
  function renderRow(identifier: AuthnProfileIdentifier, index: number) {
    const value = identifier.value ?? ''
    // `proven` is the server's derivation of whether the way the identifier
    // was established proves ownership; `verified_at` is present on every
    // stored row and cannot answer that question.
    const verified = identifier.proven === true
    return (
      <Box
        component="li"
        key={identifier.id ?? index}
        sx={{
          py: 1.5,
          minWidth: 0,
          ...(index > 0
            ? { borderTop: '1px solid', borderColor: 'divider' }
            : {}),
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="body1" noWrap sx={{ fontWeight: 500 }}>
              {value}
            </Typography>
          </Box>
          <Box
            sx={{
              marginLeft: 'auto',
              flexShrink: 0,
              display: 'flex',
              alignItems: 'center',
              gap: 1,
            }}
          >
            <Chip
              size="small"
              variant="outlined"
              color={verified ? 'primary' : 'default'}
              label={
                verified ? t('identifiers.verified') : t('identifiers.unverified')
              }
            />
          </Box>
        </Box>
      </Box>
    )
  }

  return (
    <SectionCard title={t('contacts.title')}>
      <AccountAsyncSection
        pending={pending}
        payload={identifiers}
        empty={false}
        error={error}
        refreshing={isFetching}
        loading={<ContactListSkeleton label={t('contacts.loading')} />}
        errorState={{
          title: t('contacts.error.title'),
          description: t('contacts.error.description'),
          retryLabel: t('contacts.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {(settled) => (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {kinds.map((kind) => {
              const groupRows = settled.filter((row) => row.kind === kind)
              const headingId = `${groupIdBase}-${kind}`
              return (
                <Box
                  component="section"
                  key={kind}
                  aria-labelledby={headingId}
                >
                  <Typography
                    id={headingId}
                    component="h3"
                    variant="subtitle1"
                    sx={{ mb: 0.5 }}
                  >
                    {t(`identifiers.kind.${kind}`)}
                  </Typography>
                  {groupRows.length === 0 ? (
                    <Typography variant="body2" color="text.secondary">
                      {t('contacts.none')}
                    </Typography>
                  ) : (
                    <Box
                      component="ul"
                      role="list"
                      sx={{ m: 0, p: 0, listStyle: 'none' }}
                    >
                      {groupRows.map((identifier, index) =>
                        renderRow(identifier, index),
                      )}
                    </Box>
                  )}
                </Box>
              )
            })}
          </Box>
        )}
      </AccountAsyncSection>
    </SectionCard>
  )
}
