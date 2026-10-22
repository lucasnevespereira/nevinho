# Terminal hooks

Add `~/.nevinho/hooks.json` to run commands when `nevinho chat` changes state. Hooks do not run in the Discord daemon. Nevinho loads the file at startup, so restart it after editing.

Each event maps to a list of commands. A command is an array containing the executable and its arguments:

```json
{
  "turn_finished": [["/usr/bin/logger", "nevinho finished a turn"]]
}
```

## Events

| Event | When it fires | Extra JSON fields |
| --- | --- | --- |
| `turn_started` | A prompt starts or resumes after an approval decision | None |
| `approval_requested` | A tool call needs your decision | None |
| `turn_finished` | A turn completes, fails, or is cancelled | `outcome`: `completed`, `error`, or `cancelled` |
| `tool_started` | A tool call starts | `tool` |
| `tool_finished` | A tool call ends | `tool`, `status`: `ok`, `failed`, `blocked`, or `needs_approval` |
| `session_exited` | The terminal client exits | None |

Each command receives one JSON object on standard input with `event` and a UTC `timestamp`, plus the fields above when applicable. Prompt text and tool input or output are not included. Commands inherit Nevinho's environment and working directory. Arguments are passed literally. Use an explicit shell command if you need shell expansion.

Hooks run in event order outside the UI loop, with a five-second timeout per command. Failures appear in `~/.nevinho/chat.log` without interrupting a turn. Invalid JSON or event names stop Nevinho at startup with an error.

## Farol example

Start Nevinho inside [Farol](https://farol.sh/docs) and use this `hooks.json` to update Farol's session status:

```json
{
  "turn_started": [["/bin/sh", "-c", "[ -z \"$FAROL_CLI\" ] || \"$FAROL_CLI\" status working"]],
  "approval_requested": [["/bin/sh", "-c", "[ -z \"$FAROL_CLI\" ] || \"$FAROL_CLI\" status waiting"]],
  "turn_finished": [["/bin/sh", "-c", "[ -z \"$FAROL_CLI\" ] || \"$FAROL_CLI\" status done"]],
  "session_exited": [["/bin/sh", "-c", "[ -z \"$FAROL_CLI\" ] || \"$FAROL_CLI\" status clear"]]
}
```

`$FAROL_CLI` is available in Farol sessions. The checks make the same file harmless when Nevinho runs in another terminal.
