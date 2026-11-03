/**
 * AccountSelector: a searchable account picker for the write and lookup
 * forms that name an account.
 *
 * An operator identifies an account by its display name or one of its
 * login identifiers, not by its opaque id. The control is a combobox: the
 * operator types, the console reads accounts through the account list
 * operation (narrowed by a display-name prefix and by each identifier
 * prefix), and a chosen row hands its account id to the caller. Nothing is
 * read before the operator types, so opening the page never pulls the
 * account table.
 *
 * The searches are debounced and page-limited. A name prefix and the
 * identifier kinds (username, email, phone) are separate list reads, so
 * their answers are merged and de-duplicated by account id. The listing
 * refuses an identifier prefix that is shorter than three characters or
 * that its kind's shape does not take, so only the kinds this text can
 * name are read; a kind the server refuses anyway is treated as not
 * applicable rather than as a failure. A failed search -- a transport or
 * server failure -- is stated in the popup rather than shown as "no
 * matches". A read that answers more rows than it was asked for leaves a
 * next page unread; the control says so instead of pretending the merged
 * rows are the whole answer, and asks the operator to narrow the text.
 *
 * The combobox is keyboard operable end to end (arrow keys move through
 * the options, Enter selects, Escape closes) and takes its accessible name
 * from the label beside it.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import Autocomplete from '@mui/material/Autocomplete'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { httpStatusOf } from '@speed/ui-kit'
import { useAdminUiTranslation } from './translation.js'
import { useAdminOperations } from '../operations.js'
import type { AdminAccount } from '../operations.js'

/** How many rows each search read asks for. */
const SEARCH_LIMIT = 20

/** The pause between the last keystroke and the read it triggers. */
const SEARCH_DEBOUNCE_MS = 250

/** The shortest identifier prefix the account listing accepts. */
const MIN_IDENTIFIER_PREFIX_CHARS = 3

/** The longest an email prefix may be; the listing's own bound is 254 bytes. */
const EMAIL_PREFIX_MAX_BYTES = 254

/** The username prefix shape the listing accepts, after folding. */
const USERNAME_PREFIX_SHAPE = /^[a-z0-9][a-z0-9._-]{0,63}$/

/** The phone prefix shape the listing accepts: the head of an E.164 number. */
const PHONE_PREFIX_SHAPE = /^\+[1-9][0-9]{0,14}$/

/** The rows the open search has settled, and its progress. */
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

/** The option's text: the display name beside the id, or the id alone. */
function accountOptionLabel(account: AdminAccount): string {
  return account.display_name === ''
    ? account.id
    : `${account.display_name} (${account.id})`
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
 * Which identifier reads the account listing accepts for this text.
 *
 * The listing normalizes a prefix before comparing it -- a username is
 * NFKC-folded and lower-cased, an email trimmed and lower-cased -- and
 * refuses a prefix shorter than three characters or one its kind's shape
 * does not take: a username of [a-z0-9._-] beginning with a letter or a
 * digit, an email of visible ASCII with at most one @, a phone the head of
 * an E.164 number. The rules are pkg/authn/account's; mirroring them here
 * keeps a search from issuing a read the route answers 400. Username and
 * phone are mutually exclusive shapes; an ASCII word is both a username
 * and an email prefix, so those two reads may both run.
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

/**
 * Whether a read failed because the listing refused its shape: HTTP 400,
 * the answer the account listing gives a prefix it cannot read. That
 * refusal says the kind does not apply to this text; a transport failure,
 * a 5xx or any other answer says the search itself failed. The status is
 * read through ui-kit's `httpStatusOf`, which also recognizes the
 * `client.http.400` an envelope-less refusal is synthesized as.
 */
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
   * Whether the picker is disabled. A page disables it while a scope the
   * chosen account drives has a write in flight: a change would reset that
   * scope and leave a settling attempt describing the account left behind.
   */
  readonly disabled?: boolean
  /**
   * Whether the combobox fills its container. A caller that places it in a
   * narrow row (the sessions page's selector row) sets this so the row
   * shrinks instead of wrapping; the stock form keeps its fixed minimum.
   */
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
  const selectedLabel = value === null ? '' : accountOptionLabel(value)

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
        getOptionLabel={accountOptionLabel}
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
 * AccountSearch: the reads behind one account query. It is mounted only
 * while there is text to search, so an idle selector issues no request. A
 * display-name prefix and the identifier kinds the text can name are
 * separate reads; their pages are merged by account id.
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
    // A read stops at SEARCH_LIMIT and hands back a cursor for the rest.
    // The merged rows cannot tell the operator the search is exhausted, so
    // any cursor is reported: they narrow the text rather than assume the
    // first page is the answer.
    const truncated = pages.some(
      (page) => page !== undefined && page.next_cursor !== '',
    )
    // A kind the text cannot name is declared disabled, and a disabled
    // query reports pending forever (pending with nothing fetching):
    // progress is read from isLoading (pending and fetching) so it clears
    // when no read is left.
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
