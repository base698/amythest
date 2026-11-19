# Investigate tasks with an agent

In `amy tasks` (or Tasks inside Amy), press **m** to mark tasks, **M** to mark or
clear all tasks in the current query, then **a** to review the marked group.
Board-managed checkboxes are excluded. Marking is separate from Space, which
still completes/reopens a task. Groups are limited to 50 tasks per investigation.

Enter an optional prompt, such as “Look for evidence these tasks have completed.”
Leave it blank for the built-in evidence-triage prompt. Enter continues; Tab
chooses Claude or Codex; Enter starts the investigation. It runs the installed,
authenticated CLI on this machine, from Amy's working directory. It does not send
the request to a currently busy Herdr tab or open another browser window.

The CLI receives each selected task and nearby note context. It is instructed to
investigate without making changes, cite real sources, and report when evidence
is unavailable. Claude uses plan permissions with read tools; Codex uses its
read-only sandbox. Neither invocation bypasses permissions. Existing configured
connections determine which evidence the agent can actually access. Findings
are suggestions for review, not proof that work is complete.

Findings are saved as quoted context directly below the corresponding checkboxes,
including agent/date and sources. They appear under tasks in Amy and the web
Tasks view, and in the original Markdown note. Enter opens the full note at the selected task; Esc returns to the same task list.
Task statuses remain unchanged: use Space to complete, or D to cancel and then
D again to permanently delete. Deleting a cancelled task also removes generated
context; ordinary handwritten child notes remain.

Amy checks the original note version before starting and when saving. A changed
note is left untouched and reported by name. Several findings in one note are
saved together; different notes can succeed or conflict independently. A private
recovery copy of the findings is written under the OS cache directory at
`amythest/reviews/`. Esc cancels an investigation or returns from its results.

Claude's exact read-only MCP tool names can be added in the private file
`~/.config/amythest/review.json` (or under `XDG_CONFIG_HOME`):

```json
{"allowed_tools": ["mcp__your_server__search", "mcp__your_server__read"]}
```

Use names from your own Claude connection. Missing tools or denied permissions
must be reported as unavailable evidence. Account details, note text, connector
configuration, and review recovery files do not belong in the source repository.
