/**
 * AccountSelector: a searchable picker reading accounts by display name or login
 * identifier after typing, with debounced, page-limited merged searches.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import Autocomplete from '@mui/material/Autocomplete'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { httpStatusOf } from '@speed/ui-kit'
import { useAdminUiTranslation } from './translation.js'
import { accountUsername, identityLabel } from './account-identity.js'
import { useAdminOperations } from '../operations.js'
import type { AdminAccount } from '../operations.js'

const SEARCH_LIMIT = 20

const SEARCH_DEBOUNCE_MS = 250

/** The shortest identifier prefix the account listing accepts. */
const MIN_IDENTIFIER_PREFIX_CHARS = 3

/** The longest an email prefix may be; the listing's own bound is 254 bytes. */
const EMAIL_PREFIX_MAX_BYTES = 254

const USERNAME_PREFIX_SHAPE = /^[a-z0-9][a-z0-9._-]{0,63}$/

const PHONE_PREFIX_SHAPE = /^\+[1-9][0-9]{0,14}$/

interface AccountSearchState {
  readonly rows: readonly AdminAccount[]
  readonly pending: boolean
  readonly failed: boolean
  /** A read answered a full page and left a next page unread. */
  readonly truncated: boolean
}

const EMPTY_SEARCH: AccountSearchState = {
  rows: [],
  pending: false,
  failed: false,
  truncated: false,
}

function accountOptionLabel(
  t: ReturnType<typeof useAdminUiTranslation>['t'],
  account: AdminAccount,
): string {
  return (
    identityLabel(account.display_name, accountUsername(account.identifiers)) ??
    t('common.accountUnavailable')
  )
}

/** Whether every character is visible ASCII, the listing's email alphabet. */
function printableASCII(text: string): boolean {
  for (let index = 0; index < text.length; index += 1) {
    const code = text.charCodeAt(index)
    if (code < 0x21 || code > 0x7e) {
      return false
    }
  }
  return true
}

/** The identifier kinds an account search reads for its text. */
interface IdentifierReads {
  readonly username: boolean
  readonly email: boolean
  readonly phone: boolean
}

/**
 * Which identifier reads the listing accepts for this text. The shapes mirror
 * pkg/authn/account's rules, so a search never issues a read the route answers 400.
 */
function identifierReadsFor(query: string): IdentifierReads {
  const username = query.normalize('NFKC').toLowerCase()
  const email = query.trim().toLowerCase()
  return {
    username:
      username.length >= MIN_IDENTIFIER_PREFIX_CHARS &&
      USERNAME_PREFIX_SHAPE.test(username),
    email:
      email.length >= MIN_IDENTIFIER_PREFIX_CHARS &&
      email.length <= EMAIL_PREFIX_MAX_BYTES &&
      printableASCII(email) &&
      email.split('@').length <= 2,
    phone:
      query.length >= MIN_IDENTIFIER_PREFIX_CHARS &&
      PHONE_PREFIX_SHAPE.test(query),
  }
}

function isShapeRefusal(error: unknown): boolean {
  return httpStatusOf(error) === 400
}

export interface AccountSelectorProps {
  /** The visible label, which also names the combobox. */
  readonly label: string
  /** The chosen account, or null while nothing is chosen. */
  readonly value: AdminAccount | null
  /** Called with the chosen account, or null when the choice is cleared. */
  readonly onChange: (account: AdminAccount | null) => void
  /**
   * Whether the picker is disabled. It stays natively disabled while a write
   * into a scope the chosen account addresses is in flight; the write starts
   * from another control, so focus is not on the picker.
   */
  readonly disabled?: boolean
  /** Whether the combobox fills its container, for a narrow row. */
  readonly fullWidth?: boolean
}

export function AccountSelector({
  label,
  value,
  onChange,
  disabled = false,
  fullWidth = false,
}: AccountSelectorProps) {
  const { t } = useAdminUiTranslation()
  const [input, setInput] = useState('')
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState<AccountSearchState>(EMPTY_SEARCH)

  // The reads settle one after another; a settlement that leaves the
  // merged answer unchanged is dropped, so the page re-renders on a real
  // change rather than once per read.
  const applySearch = useCallback((next: AccountSearchState) => {
    setSearch((previous) =>
      previous.pending === next.pending &&
      previous.failed === next.failed &&
      previous.truncated === next.truncated &&
      previous.rows.length === next.rows.length &&
      previous.rows.every((row, index) => row.id === next.rows[index]?.id)
        ? previous
        : next,
    )
  }, [])

  // The text a chosen account fills into the box is not a search: it names
  // the selection already made.
  const selectedLabel = value === null ? '' : accountOptionLabel(t, value)

  // A caller that clears the choice after a landed write clears the box
  // too, so the box never shows a label the control no longer holds.
  const previousValue = useRef<AdminAccount | null>(null)
  useEffect(() => {
    if (value === null && previousValue.current !== null) {
      setInput('')
    }
    previousValue.current = value
  }, [value])

  useEffect(() => {
    const trimmed = input.trim()
    const timer = setTimeout(() => {
      setQuery(trimmed === '' || trimmed === selectedLabel ? '' : trimmed)
    }, SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [input, selectedLabel])

  const searching = query !== '' && search.pending

  return (
    <Box>
      <Autocomplete<AdminAccount>
        size="small"
        value={value}
        disabled={disabled}
        onChange={(_event, next) => onChange(next)}
        inputValue={input}
        onInputChange={(_event, nextValue) => setInput(nextValue)}
        options={query === '' ? EMPTY_SEARCH.rows : search.rows}
        loading={searching}
        filterOptions={(options) => options}
        getOptionLabel={(account) => accountOptionLabel(t, account)}
        isOptionEqualToValue={(option, current) => option.id === current.id}
        noOptionsText={
          query === ''
            ? t('accountSelector.hint')
            : search.failed
              ? t('accountSelector.failed')
              : t('accountSelector.empty')
        }
        loadingText={t('accountSelector.searching')}
        clearText={t('accountSelector.clear')}
        openText={t('accountSelector.open')}
        closeText={t('accountSelector.close')}
        renderInput={(params) => (
          <TextField
            {...params}
            size="small"
            label={label}
            slotProps={{
              ...params.slotProps,
              input: {
                ...params.slotProps.input,
                endAdornment: (
                  <>
                    {searching ? (
                      <CircularProgress
                        aria-label={t('accountSelector.searching')}
                        color="inherit"
                        size={16}
                      />
                    ) : null}
                    {params.slotProps.input.endAdornment}
                  </>
                ),
              },
            }}
          />
        )}
        sx={
          fullWidth
            ? { minWidth: 0, width: '100%' }
            : { minWidth: 260 }
        }
      />
      {query !== '' && search.truncated && !search.failed && (
        <Typography
          component="p"
          variant="caption"
          color="text.secondary"
          sx={{ display: 'block', mt: 0.5 }}
        >
          {t('accountSelector.narrow')}
        </Typography>
      )}
      {query !== '' && <AccountSearch query={query} onState={applySearch} />}
    </Box>
  )
}

/**
 * AccountSearch: the reads behind one account query, mounted only while there
 * is text to search; the display-name and identifier pages are merged by id.
 */
function AccountSearch({
  query,
  onState,
}: {
  readonly query: string
  readonly onState: (state: AccountSearchState) => void
}) {
  const operations = useAdminOperations()
  const identifierReads = identifierReadsFor(query)
  const byName = operations.useAccounts({
    limit: SEARCH_LIMIT,
    display_name_prefix: query,
  })
  const byUsername = operations.useAccounts(
    {
      limit: SEARCH_LIMIT,
      identifier_prefix: query,
      identifier_kind: 'username',
    },
    { enabled: identifierReads.username },
  )
  const byEmail = operations.useAccounts(
    {
      limit: SEARCH_LIMIT,
      identifier_prefix: query,
      identifier_kind: 'email',
    },
    { enabled: identifierReads.email },
  )
  const byPhone = operations.useAccounts(
    {
      limit: SEARCH_LIMIT,
      identifier_prefix: query,
      identifier_kind: 'phone',
    },
    { enabled: identifierReads.phone },
  )

  const state = useMemo<AccountSearchState>(() => {
    const rows: AdminAccount[] = []
    const seen = new Set<string>()
    const pages = [byName.data, byUsername.data, byEmail.data, byPhone.data]
    for (const page of pages) {
      for (const account of page?.accounts ?? []) {
        if (!seen.has(account.id)) {
          seen.add(account.id)
          rows.push(account)
        }
      }
    }
    // A read stops at SEARCH_LIMIT and hands back a cursor; any cursor means
    // the merged rows are not the whole answer, so the operator narrows.
    const truncated = pages.some(
      (page) => page !== undefined && page.next_cursor !== '',
    )
    // A disabled query reports pending forever, so progress is read from
    // isLoading, which clears once no read is left.
    const pending =
      byName.isLoading ||
      byUsername.isLoading ||
      byEmail.isLoading ||
      byPhone.isLoading
    const failed =
      rows.length === 0 &&
      (byName.isError ||
        (byUsername.isError && !isShapeRefusal(byUsername.error)) ||
        (byEmail.isError && !isShapeRefusal(byEmail.error)) ||
        (byPhone.isError && !isShapeRefusal(byPhone.error)))
    return { rows, pending, failed, truncated }
  }, [
    byName.data,
    byName.isLoading,
    byName.isError,
    byUsername.data,
    byUsername.isLoading,
    byUsername.isError,
    byUsername.error,
    byEmail.data,
    byEmail.isLoading,
    byEmail.isError,
    byEmail.error,
    byPhone.data,
    byPhone.isLoading,
    byPhone.isError,
    byPhone.error,
  ])

  useEffect(() => {
    onState(state)
  }, [state, onState])

  return null
}
