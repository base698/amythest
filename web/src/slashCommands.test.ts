import test from "node:test"
import assert from "node:assert/strict"
import { localDateStamp, matchSlashCommands, parseSlashQuery, slashCommands } from "./slashCommands.ts"

test("parseSlashQuery only treats leading-slash queries as commands", () => {
  assert.equal(parseSlashQuery("hello"), null)
  assert.equal(parseSlashQuery(""), null)
  assert.equal(parseSlashQuery("/"), "")
  assert.equal(parseSlashQuery("/today"), "today")
  assert.equal(parseSlashQuery("/ today"), "today")
  // A slash later in the query is a search, not a command.
  assert.equal(parseSlashQuery("notes/today"), null)
})

test("matchSlashCommands narrows by prefix and lists all for empty", () => {
  assert.equal(matchSlashCommands("").length, slashCommands.length)
  const hits = matchSlashCommands("tod")
  assert.equal(hits.length, 1)
  assert.equal(hits[0].name, "today")
  assert.equal(matchSlashCommands("TOD").length, 1)
  assert.equal(matchSlashCommands("zzz").length, 0)
})

test("/today points at today's daily note under the site base", () => {
  const today = slashCommands.find((c) => c.name === "today")!
  const href = today.href({ base: "/notes/", now: new Date(2026, 7, 10, 8, 30) })
  assert.equal(href, "/notes/Daily-Notes/2026-08-10")
})

test("localDateStamp uses local time, not UTC", () => {
  // 11pm local on Aug 9 must stamp Aug 9 even if UTC has rolled over.
  assert.equal(localDateStamp(new Date(2026, 7, 9, 23, 0)), "2026-08-09")
  assert.equal(localDateStamp(new Date(2026, 0, 1, 0, 5)), "2026-01-01")
})
