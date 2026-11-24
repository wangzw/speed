/**
 * File-input and drag-and-drop test helpers. jsdom ships neither
 * `DataTransfer` nor `DragEvent`, so `fireEvent` is what copies the files
 * onto the node and the event.
 */

import { fireEvent } from '@testing-library/react'

export function selectFiles(input: HTMLInputElement, files: readonly File[]): void {
  fireEvent.change(input, { target: { files } })
}

export function dropFiles(target: Element, files: readonly File[]): void {
  fireEvent.drop(target, { dataTransfer: { files } } as Parameters<
    typeof fireEvent.drop
  >[1])
}
