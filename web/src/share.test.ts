import assert from "node:assert/strict"
import test from "node:test"

import { buildTextNotePayload, shareMutationHeaders, shareSessionEndpoint, shareTextDescriptionSelector, shareTextEndpoint, shareTextSaveSelector, shareTextTitleSelector } from "./share.ts"

test("text share UI contract names fields, action, and endpoint", () => {
  assert.equal(shareTextTitleSelector, "#share-text-title")
  assert.equal(shareTextDescriptionSelector, "#share-text-description")
  assert.equal(shareTextSaveSelector, "#share-text-save")
  assert.equal(shareTextEndpoint, "api/share/text")
  assert.equal(shareSessionEndpoint, "kanban/api/session")
})

test("share mutation headers carry the current session CSRF token", () => {
  assert.deepEqual(shareMutationHeaders("fresh-token"), { "X-CSRF-Token": "fresh-token" })
  assert.deepEqual(shareMutationHeaders("fresh-token", true), {
    "Content-Type": "application/json",
    "X-CSRF-Token": "fresh-token",
  })
})

test("buildTextNotePayload requires and trims title while preserving description", () => {
  assert.deepEqual(buildTextNotePayload("  Trip reminder  ", "Pack chargers.\n\nLeave by 8."), {
    title: "Trip reminder",
    description: "Pack chargers.\n\nLeave by 8.",
  })
  assert.throws(() => buildTextNotePayload("   ", "optional"), /title/i)
})
