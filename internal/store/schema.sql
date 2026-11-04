CREATE TABLE IF NOT EXISTS books (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'personal',
    currency    TEXT NOT NULL DEFAULT 'CNY',
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS categories (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    direction   TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    name        TEXT NOT NULL,
    sort        INTEGER NOT NULL DEFAULT 0,
    is_system   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS projects (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id         INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    client_name     TEXT NOT NULL DEFAULT '',
    contract_amount INTEGER NOT NULL DEFAULT 0,
    designer        TEXT NOT NULL DEFAULT '',
    manager_name    TEXT NOT NULL DEFAULT '',
    manager_amount  INTEGER NOT NULL DEFAULT 0,
    start_date      TEXT NOT NULL DEFAULT '',
    planned_end_date TEXT NOT NULL DEFAULT '',
    actual_end_date TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'signed',
    note            TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    deleted_at      TEXT
);

CREATE TABLE IF NOT EXISTS transactions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id      INTEGER NOT NULL REFERENCES books(id),
    project_id   INTEGER REFERENCES projects(id),
    direction    TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    category_id  INTEGER REFERENCES categories(id),
    amount       INTEGER NOT NULL CHECK (amount > 0),
    occurred_on  TEXT NOT NULL,
    counterparty TEXT NOT NULL DEFAULT '',
    pay_method   TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    deleted_at   TEXT
);

CREATE TABLE IF NOT EXISTS attachments (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    object_key     TEXT NOT NULL,
    mime           TEXT NOT NULL DEFAULT '',
    size           INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tx_book_date ON transactions(book_id, occurred_on);
CREATE INDEX IF NOT EXISTS idx_tx_project   ON transactions(project_id);
CREATE INDEX IF NOT EXISTS idx_att_tx       ON attachments(transaction_id);
CREATE INDEX IF NOT EXISTS idx_proj_book    ON projects(book_id);
