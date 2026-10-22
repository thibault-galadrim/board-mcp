CREATE TABLE meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE projects (
	slug TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	created_by TEXT NOT NULL
);

CREATE TABLE tickets (
	id INTEGER PRIMARY KEY,
	project TEXT REFERENCES projects(slug),
	kind TEXT NOT NULL CHECK (kind IN ('task', 'reminder')),
	title TEXT NOT NULL,
	details TEXT NOT NULL DEFAULT '',
	priority INTEGER NOT NULL DEFAULT 2 CHECK (priority BETWEEN 0 AND 3),
	due TEXT,
	state TEXT NOT NULL CHECK (state IN ('inbox', 'todo', 'in_progress', 'blocked', 'to_validate', 'done', 'dropped')),
	owner TEXT,
	parent_id INTEGER REFERENCES tickets(id),
	links TEXT NOT NULL DEFAULT '[]',
	created_by TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	owner_activity_at TEXT,
	CHECK (kind = 'reminder' OR project IS NOT NULL)
);
CREATE INDEX tickets_project_state ON tickets(project, state);

-- Who created, dispatched or took a ticket: only they (and the owner) decide its fate.
CREATE TABLE ticket_circle (
	ticket_id INTEGER NOT NULL REFERENCES tickets(id),
	name TEXT NOT NULL,
	PRIMARY KEY (ticket_id, name)
) WITHOUT ROWID;

-- Append-only journal.
CREATE TABLE events (
	id INTEGER PRIMARY KEY,
	ticket_id INTEGER REFERENCES tickets(id),
	at TEXT NOT NULL,
	actor TEXT NOT NULL,
	kind TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	data TEXT NOT NULL DEFAULT '{}',
	proof TEXT
);
CREATE INDEX events_ticket ON events(ticket_id, id);

CREATE TABLE actors (
	name TEXT PRIMARY KEY,
	identity TEXT NOT NULL,
	bound_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL
);

CREATE TABLE cursors (
	actor TEXT NOT NULL,
	scope TEXT NOT NULL,
	event_id INTEGER NOT NULL,
	PRIMARY KEY (actor, scope)
) WITHOUT ROWID;

CREATE TABLE requests (
	id INTEGER PRIMARY KEY,
	ticket_id INTEGER REFERENCES tickets(id),
	action TEXT NOT NULL,
	actor TEXT NOT NULL,
	identity TEXT NOT NULL,
	op TEXT NOT NULL,
	summary TEXT NOT NULL,
	state TEXT NOT NULL CHECK (state IN ('pending', 'approved', 'refused', 'failed')),
	outcome TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	decided_at TEXT
);
CREATE INDEX requests_state ON requests(state, ticket_id);
