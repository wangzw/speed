/**
 * WriteFeedbackRegion: the console's binding of the shared region.
 *
 * The region itself is ui-kit's; this binding exists so a failure code
 * resolves through the console-ui namespace's whitelist and nothing else.
 * The cases below drive a real scope slot: a whitelisted code renders its
 * own text, while a plain throw collapses through `errorCodeOf` to a code
 * no resolver lists and renders the shared unknown fallback -- never a
 * raw key.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useWriteFeedback } from '@speed/ui-kit'
import { WriteFeedbackRegion } from './write-feedback.js'
import { renderWithProviders } from '../../test-utils/render.js'

/** A probe that settles the scope's slot with a scripted attempt. */
function Probe({ fail }: { readonly fail: () => Promise<void> }) {
  const feedback = useWriteFeedback()
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          void feedback.run(async () => {
            await fail()
            return '写入成功'
          })
        }}
      >
        write
      </button>
      <WriteFeedbackRegion outcome={feedback.outcome} />
    </div>
  )
}

/** A probe that runs two attempts the test settles by hand. */
function OverlapProbe({
  first,
  second,
}: {
  readonly first: () => Promise<string | null>
  readonly second: () => Promise<string | null>
}) {
  const feedback = useWriteFeedback()
  return (
    <div>
      <button type="button" onClick={() => void feedback.run(first)}>
        first
      </button>
      <button type="button" onClick={() => void feedback.run(second)}>
        second
      </button>
      <WriteFeedbackRegion outcome={feedback.outcome} />
    </div>
  )
}

describe('WriteFeedbackRegion', () => {
  it('resolves a whitelisted failure code through the console namespace', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <Probe fail={() => Promise.reject({ code: 'authz.forbidden' })} />,
    )
    await user.click(screen.getByRole('button', { name: 'write' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      '你没有执行此操作的权限。',
    )
  })

  it('renders the unknown fallback for a plain throw, never a raw key', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Probe fail={() => Promise.reject(new Error('boom'))} />)
    await user.click(screen.getByRole('button', { name: 'write' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('出了点问题，请稍后重试。')
    // `errorCodeOf` collapses the throw to client.unknown; the resolver
    // carries no leaf for it, so the fallback stands in -- the raw code
    // must never reach the banner.
    expect(alert).not.toHaveTextContent('client.unknown')
  })

  it('announces a settled success in the polite live region', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Probe fail={() => Promise.resolve()} />)
    await user.click(screen.getByRole('button', { name: 'write' }))
    expect(await screen.findByText('写入成功')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('keeps the attempt that settles last and leaves the slot empty on null', async () => {
    const user = userEvent.setup()
    let settleFirst!: (message: string | null) => void
    let settleSecond!: (message: string | null) => void
    const first = () =>
      new Promise<string | null>((resolve) => {
        settleFirst = resolve
      })
    const second = () =>
      new Promise<string | null>((resolve) => {
        settleSecond = resolve
      })
    renderWithProviders(<OverlapProbe first={first} second={second} />)

    await user.click(screen.getByRole('button', { name: 'first' }))
    await user.click(screen.getByRole('button', { name: 'second' }))
    // The second attempt settles first; the first settles last and wins.
    settleSecond('second message')
    expect(await screen.findByText('second message')).toBeInTheDocument()
    settleFirst('first message')
    expect(await screen.findByText('first message')).toBeInTheDocument()
    expect(screen.queryByText('second message')).toBeNull()

    // A null settlement -- the elevation gate took the write over -- does
    // not touch the slot: the attempt's own reset leaves it empty.
    await user.click(screen.getByRole('button', { name: 'first' }))
    settleFirst(null)
    await vi.waitFor(() => {
      expect(screen.queryByText('first message')).toBeNull()
    })
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
