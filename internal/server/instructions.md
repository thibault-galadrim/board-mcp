board is the shared task board of the owner and every Claude Code session on this machine.

Identity: pass `as` on every call, a stable lowercase session name (e.g. "coord-atlas", "fix-login"). Keep it for the whole session, also after a compaction. The board checks which Claude process you are: you cannot act under a name held by another live session.

Projects: always name the project explicitly, from the conversation. Never derive it from the folder. If unsure, ask the owner. Never create a project unless the owner says so; the owner approves it anyway.

Working on tickets:
- Call board_whatsnew(project) when you start, after a compaction, and before giving the owner a status update.
- board_next(project) gives you the most urgent todo task of that project and marks it in progress for you. Only take tasks of the project you are working on.
- Write progress notes with board_note: they are the ticket's journal (decisions, PR links, what is left).
- Blocked, or need the owner: board_note(state="blocked", text=<your question>).
- Finished: board_note(state="to_validate", text=<summary>, links=[<PR>]). Use state="done" only when the owner asks you to close it. If the owner sends it back (event "sent_back"), their note says what is missing: resume from there.
- Already working on something that is not on the board: board_add it with doing=true (in progress, yours), then keep its journal up to date.
- A ticket sits in the wrong project: board_move it.
- Found something out of scope (bug, debt, idea): board_add it. It lands in the owner's inbox; do not work on it.
- When the owner asks you to note a task for them or remind them of something, board_add it with kind="reminder". Reminders are the owner's own tasks: never execute them, and mention the due ones in your status updates.
- When the owner asks you to put a task on the board for sessions, board_add it with todo=true.

Approvals: depending on the project's mode, some actions need the owner's approval. If the owner asked you for this exact action in this conversation, pass owner_asked=true: they get the Touch ID prompt right away and your call succeeds once they approve (if they refuse, do not insist). Never set owner_asked otherwise. Without it, or if they do not answer in time, the call returns status "pending_approval" with a request id: the owner is notified; run `board wait <id>` in the background (it exits once they decide) instead of retrying, and carry on with something else meanwhile.

If the board says it is locked, tell the owner to run `board unlock`.

Coordinating other sessions (optional): board_claim(id, for=<name>) takes a ticket for another session, and board_add(parent_id=<id>) splits work into sub-tickets. Only the sessions that created, dispatched or took a ticket (its circle) decide its fate alone; anyone may comment.
