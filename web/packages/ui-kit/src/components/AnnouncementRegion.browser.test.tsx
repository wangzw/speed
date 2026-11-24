
import { describe, expect, it } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithProviders } from '../../test-utils/render.js'
import { ActionButton, ActionRow } from './ActionRow.js'
import { AnnouncementRegion } from './AnnouncementRegion.js'

const REGION_ID = 'browser-announcement-region'
const MESSAGE = 'The status write finished'

function regionNode(): HTMLElement {
  const node = document.getElementById(REGION_ID)
  expect(node).not.toBeNull()
  return node as HTMLElement
}

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
      expect(node.textContent).toBe('')

      expect(await framesUntilText(node, MESSAGE)).toBeGreaterThanOrEqual(2)

      await waitFor(() => expect(snapshots.length).toBeGreaterThan(0))
      expect(snapshots[0]).toBe('')
      expect(snapshots).toContain(MESSAGE)
    } finally {
      observer.disconnect()
    }
  })

  it('arms a second frame and cancels it when the region unmounts', async () => {
    const realRequest = globalThis.requestAnimationFrame.bind(globalThis)
    const realCancel = globalThis.cancelAnimationFrame.bind(globalThis)
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

      await new Promise<void>((resolve) => {
        realRequest(() => resolve())
      })
      expect([...live]).toHaveLength(1)

      view.unmount()

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
