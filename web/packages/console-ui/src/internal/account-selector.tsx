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
 * The searches are debounced and page-limited. A name prefix and the three
 * identifier kinds (username, email, phone) are separate list reads, so
 * their answers are merged and de-duplicated by account id. A failed
 * search is stated in the popup rather than shown as "no matches".
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
import { useConsoleUiTranslation } from './translation.js'
import { useConsoleOperations } from '../operations.js'
import type { ConsoleAccount } from '../operations.js'

/** How many rows each search read asks for. */
const SEARCH_LIMIT = 20

/** The pause between the last keystroke and the read it triggers. */
const SEARCH_DEBOUNCE_MS = 250

/** The rows the open search has settled, and its progress. */
interface AccountSearchState {
  readonly rows: readonly ConsoleAccount[]
  readonly pending: boolean
  readonly failed: boolean
}

const EMPTY_SEARCH: AccountSearchState = {
  rows: [],
  pending: false,
  failed: false,
}

/** The option's text: the display name beside the id, or the id alone. */
function accountOptionLabel(account: ConsoleAccount): string {
  return account.display_name === ''
    ? account.id
    : `${account.display_name} (${account.id})`
}

export interface AccountSelectorProps {
  /** The visible label, which also names the combobox. */
  readonly label: string
  /** The chosen account, or null while nothing is chosen. */
  readonly value: ConsoleAccount | null
  /** Called with the chosen account, or null when the choice is cleared. */
  readonly onChange: (account: ConsoleAccount | null) => void
}

export function AccountSelector({
  label,
  value,
  onChange,
}: AccountSelectorProps) {
  const { t } = useConsoleUiTranslation()
  const [input, setInput] = useState('')
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState<AccountSearchState>(EMPTY_SEARCH)

  // The four reads settle one after another; a settlement that leaves the
  // merged answer unchanged is dropped, so the page re-renders on a real
  // change rather than once per read.
  const applySearch = useCallback((next: AccountSearchState) => {
    setSearch((previous) =>
      previous.pending === next.pending &&
      previous.failed === next.failed &&
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
  const previousValue = useRef<ConsoleAccount | null>(null)
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
      <Autocomplete<ConsoleAccount>
        size="small"
        value={value}
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
                      <CircularProgress color="inherit" size={16} />
                    ) : null}
                    {params.slotProps.input.endAdornment}
                  </>
                ),
              },
            }}
          />
        )}
        sx={{ minWidth: 260 }}
      />
      {query !== '' && <AccountSearch query={query} onState={applySearch} />}
    </Box>
  )
}

/**
 * AccountSearch: the reads behind one account query. It is mounted only
 * while there is text to search, so an idle selector issues no request. A
 * display-name prefix and the three identifier kinds are separate reads;
 * their pages are merged by account id.
 */
function AccountSearch({
  query,
  onState,
}: {
  readonly query: string
  readonly onState: (state: AccountSearchState) => void
}) {
  const operations = useConsoleOperations()
  const byName = operations.useAccounts({
    limit: SEARCH_LIMIT,
    display_name_prefix: query,
  })
  const byUsername = operations.useAccounts({
    limit: SEARCH_LIMIT,
    identifier_prefix: query,
    identifier_kind: 'username',
  })
  const byEmail = operations.useAccounts({
    limit: SEARCH_LIMIT,
    identifier_prefix: query,
    identifier_kind: 'email',
  })
  const byPhone = operations.useAccounts({
    limit: SEARCH_LIMIT,
    identifier_prefix: query,
    identifier_kind: 'phone',
  })

  const state = useMemo<AccountSearchState>(() => {
    const rows: ConsoleAccount[] = []
    const seen = new Set<string>()
    for (const page of [byName.data, byUsername.data, byEmail.data, byPhone.data]) {
      for (const account of page?.accounts ?? []) {
        if (!seen.has(account.id)) {
          seen.add(account.id)
          rows.push(account)
        }
      }
    }
    const pending =
      byName.isPending ||
      byUsername.isPending ||
      byEmail.isPending ||
      byPhone.isPending
    const failed =
      rows.length === 0 &&
      (byName.isError ||
        byUsername.isError ||
        byEmail.isError ||
        byPhone.isError)
    return { rows, pending, failed }
  }, [
    byName.data,
    byName.isPending,
    byName.isError,
    byUsername.data,
    byUsername.isPending,
    byUsername.isError,
    byEmail.data,
    byEmail.isPending,
    byEmail.isError,
    byPhone.data,
    byPhone.isPending,
    byPhone.isError,
  ])

  useEffect(() => {
    onState(state)
  }, [state, onState])

  return null
}
