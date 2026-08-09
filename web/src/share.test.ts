import assert from "node:assert/strict"
import test from "node:test"

import { buildTextNotePayload, shareTextDescriptionSelector, shareTextEndpoint, shareTextSaveSelector, shareTextTitleSelector } from "./share.ts"

test("text share UI contract names fields, action, and endpoint", () => {
  assert.equal(shareTextTitleSelector, "#share-text-title")
  assert.equal(shareTextDescriptionSelector, "#share-text-description")
  assert.equal(shareTextSaveSelector, "#share-text-save")
  assert.equal(shareTextEndpoint, "api/share/text")
})

test("buildTextNotePayload requires and trims title while preserving description", () => {
  assert.deepEqual(buildTextNotePayload("  Trip reminder  ", "Pack chargers.\n\nLeave by 8."), {
    title: "Trip reminder",
    description: "Pack chargers.\n\nLeave by 8.",
  })
  assert.throws(() => buildTextNotePayload("   ", "optional"), /title/i)
})
