CREATE TABLE IF NOT EXISTS canary (
    id integer PRIMARY KEY,
    note text NOT NULL
);
INSERT INTO canary (id, note)
VALUES (1, 'pannier-smoke-canary')
ON CONFLICT (id) DO UPDATE SET note = EXCLUDED.note;
