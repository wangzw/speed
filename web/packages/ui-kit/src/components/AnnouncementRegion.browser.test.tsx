/**
 * AnnouncementRegion in a real browser: mounting it in the same commit as
 * the kit's ActionButton, whose resident MUI Tooltip updates state during
 * commit, still inserts the region empty and writes its text two animation
 * frames later. The frame chain counts those frames, so an implementation
 * that writes on the first frame fails the case rather than merely being
 * "later than the commit"; the unmount case lets the second frame arm and
 * then drives more than ten, so a cleanup that cancels only the first
 * frame fails it instead of passing on a single `cancelAnimationFrame`
 * call.
 *
 * A MutationObserver records what the region's text was in every batch of
 * DOM changes after it appeared, so the birth frame being empty and the
 * text arriving afterwards are observed facts rather than a snapshot taken
 * once the case decided to look.
 */

import { describe, expect, it } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../test-utils/render.js'
import { ActionButton, ActionRow } from './ActionRow.js'
import { AnnouncementRegion } from './AnnouncementRegion.js'

const REGION_ID = 'browser-announcement-region'
const MESSAGE = 'The status write finished'

/** The region under test, read synchronously in the commit that inserted it. */
function regionNode(): HTMLElement {
  const node = document.getElementById(REGION_ID)
  expect(node).not.toBeNull()
  return node as HTMLElement
}

/**
 * Resolves with the index of the first animation frame whose callback saw
 * `text` in `node`. Frame 0 is the first callback registered after the
 * commit; the component registered its own first frame during the commit,
 * so that frame runs before frame 0.
 *
 * A two-frame implementation first shows the text at frame 2: its fill
 * callback runs in frame 1, and React writes the state in the microtask
 * checkpoint after that frame's callbacks, so frame 1 still reads empty.
 * A one-frame implementation shows it at frame 1. Asserting two or more
 * therefore tells the two apart.
 */
function framesUntilText(
  node: HTMLElement,
  text: string,
  limit = 10,
): Promise<number> {
  return new Promise((resolve, reject) => {
    let frame = 0
    const step = () => {
      if (node.textContent === text) {
        resolve(frame)
        return
      }
      if (frame >= limit) {
        reject(new Error(`the text did not appear within ${limit} frames`))
        return
      }
      frame += 1
      requestAnimationFrame(step)
    }
    requestAnimationFrame(step)
  })
}

describe('AnnouncementRegion in Chromium', () => {
  it('is born empty and first shows its text on the second frame or later beside an ActionButton mounted in the same commit', async () => {
    // Every batch of mutations after the region exists records its text.
    // The fill happens two animation frames later, in a separate task, so
    // the batch that reports the region's insertion reads it empty.
    const snapshots: string[] = []
    const observer = new MutationObserver(() => {
      const node = document.getElementById(REGION_ID)
      if (node !== null) {
        snapshots.push(node.textContent ?? '')
      }
    })
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
    })

    try {
      renderWithProviders(
        <>
          <ActionRow>
            <ActionButton unavailable unavailableReason="The tenant is saving">
              Save tenant
            </ActionButton>
          </ActionRow>
          <AnnouncementRegion id={REGION_ID} message={MESSAGE} />
        </>,
        { language: 'en-US' },
      )

      const node = regionNode()
      // The commit that inserted both the tooltip-wrapped button and the
      // region left the region empty: the Tooltip's commit-phase update
      // could not pull the scheduled write into this frame.
      expect(node.textContent).toBe('')

      // Count frames from this commit onward.
      expect(await framesUntilText(node, MESSAGE)).toBeGreaterThanOrEqual(2)

      await waitFor(() => expect(snapshots.length).toBeGreaterThan(0))
      // The observer's earliest batch that saw the region read it empty.
      expect(snapshots[0]).toBe('')
      // Some later batch carried the text, so the change reached the
      // already-inserted region rather than being born with it.
      expect(snapshots).toContain(MESSAGE)
    } finally {
      observer.disconnect()
    }
  })

  it('arms a second frame and cancels it when the region unmounts', async () => {
    const realRequest = globalThis.requestAnimationFrame.bind(globalThis)
    const realCancel = globalThis.cancelAnimationFrame.bind(globalThis)
    // Ids scheduled through the patched entry that have neither run nor
    // been cancelled; `executed` records the ones that ran.
    const live = new Set<number>()
    const executed = new Set<number>()
    const realIds = new Map<number, number>()
    let nextId = 1

    globalThis.requestAnimationFrame = (callback: FrameRequestCallback) => {
      const id = nextId++
      const realId = realRequest((time) => {
        live.delete(id)
        executed.add(id)
        callback(time)
      })
      realIds.set(id, realId)
      live.add(id)
      return id
    }
    globalThis.cancelAnimationFrame = (id: number) => {
      live.delete(id)
      const realId = realIds.get(id)
      if (realId !== undefined) {
        realCancel(realId)
      }
    }

    try {
      const view = renderWithProviders(
        <AnnouncementRegion id={REGION_ID} message={MESSAGE} />,
      )
      const node = regionNode()
      expect(node.textContent).toBe('')

      // One frame runs the component's first callback, which arms the
      // second. Waiting for it is what the old case was missing: unmounting
      // before this point leaves nothing but the already-armed first frame
      // to cancel.
      await new Promise<void>((resolve) => {
        realRequest(() => resolve())
      })
      expect([...live]).toHaveLength(1)

      view.unmount()

      // More than ten further frames never run a callback scheduled before
      // the unmount, so the write it would have made never happens, and
      // nothing scheduled is left behind. Driving the frames is what tells
      // a cleanup that cancels only the first frame apart: the armed second
      // one would run during this drive.
      const ranBeforeDriving = new Set(executed)
      await new Promise<void>((resolve) => {
        let remaining = 12
        const step = () => {
          remaining -= 1
          if (remaining <= 0) {
            resolve()
            return
          }
          realRequest(step)
        }
        realRequest(step)
      })
      expect(executed).toEqual(ranBeforeDriving)
      expect([...live]).toHaveLength(0)
      expect(node.textContent).toBe('')
    } finally {
      globalThis.requestAnimationFrame = realRequest
      globalThis.cancelAnimationFrame = realCancel
    }
  })
})
