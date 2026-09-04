---
type: project
created: 2026-07-24
updated: 2026-08-01
dispatch: true
tags: [kanban, fixture, board]
---

# Fixture Board Kanban

> Managed by Netexplore Kanban. The structured data and readable board live together in this note.

> A board used to freeze the legacy markdown format.
> Second line.

## Kanban data

<!-- AMYTHEST_KANBAN_DATA_START -->
```json
{
  "version": 2,
  "name": "fixture",
  "displayName": "Fixture Board",
  "description": "A board used to freeze the legacy markdown format.\nSecond line.",
  "icon": "🚀",
  "color": "#8b5cf6",
  "sortOrder": 2,
  "pinned": true,
  "archived": false,
  "focusCardId": "card0001",
  "dispatchEnabled": true,
  "cards": [
    {
      "id": "card0001",
      "title": "Rich card",
      "description": "Multi-line **markdown** description.\n\n- checklist item\n- [ ] task box",
      "dueDate": "2026-08-15",
      "milestone": "v2",
      "priority": "p1",
      "status": "in_progress",
      "assignee": "ada",
      "agent": "claude/opus",
      "blocked": true,
      "labels": [
        "infra",
        "urgent"
      ],
      "comments": [
        {
          "id": "cmt1",
          "author": "grace",
          "body": "Ship it\nsoon",
          "createdAt": "2026-08-01T10:30:00Z"
        }
      ],
      "attachments": [
        {
          "id": "att1",
          "filename": "spec.pdf",
          "size": 1234,
          "contentType": "application/pdf",
          "createdAt": "2026-08-01T10:30:00Z"
        }
      ],
      "audit": [
        {
          "action": "moved-board",
          "actor": "ada",
          "fromBoard": "old",
          "toBoard": "fixture",
          "createdAt": "2026-08-01T10:30:00Z"
        }
      ],
      "createdAt": "2026-08-01T10:30:00Z",
      "updatedAt": "2026-08-01T11:30:00Z"
    },
    {
      "id": "card0002",
      "title": "Plain card",
      "description": "",
      "priority": "p2",
      "status": "triage",
      "labels": [],
      "comments": [],
      "attachments": [],
      "createdAt": "2026-08-01T10:30:00Z",
      "updatedAt": "2026-08-01T10:30:00Z"
    }
  ]
}
```
<!-- AMYTHEST_KANBAN_DATA_END -->

## Triage

### Plain card ^card-card0002

- **ID:** `card0002`
- **Priority:** P2
- **Updated:** 2026-08-01T10:30:00Z

## Backlog

_No cards._

## Ready

_No cards._

## In progress

### Rich card ^card-card0001

- **ID:** `card0001`
- **Assignee:** ada
- **Agent:** claude/opus
- **Blocked:** yes
- **Due:** 2026-08-15
- **Milestone:** v2
- **Priority:** P1
- **Labels:** `infra` `urgent`
- **Updated:** 2026-08-01T11:30:00Z

Multi-line **markdown** description.

- checklist item
- [ ] task box

#### Comments

- **2026-08-01 10:30Z — grace:** Ship it soon

## Verify

_No cards._
