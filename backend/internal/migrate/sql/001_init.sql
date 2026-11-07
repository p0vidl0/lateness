CREATE TABLE schema_migrations (
    version int PRIMARY KEY,
    applied_at timestamptz NOT NULL
);

CREATE TABLE accounts (
    id uuid PRIMARY KEY,
    email text UNIQUE,
    password_hash text,
    name text NOT NULL DEFAULT '',
    is_admin boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL
);

CREATE TABLE identities (
    provider text NOT NULL,
    subject text NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts (id),
    email text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    PRIMARY KEY (provider, subject)
);

-- One identity per provider per account.
CREATE UNIQUE INDEX identities_account_provider_key ON identities (account_id, provider);

CREATE TABLE sessions (
    token_hash text PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE invites (
    id uuid PRIMARY KEY,
    token_hash text NOT NULL UNIQUE,
    note text NOT NULL DEFAULT '',
    is_admin boolean NOT NULL DEFAULT false,
    created_by uuid REFERENCES accounts (id),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    used_by uuid REFERENCES accounts (id),
    revoked_at timestamptz
);

-- Projects, outlets and expectations are never deleted: deleted_at hides them.
CREATE TABLE projects (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id),
    name text NOT NULL,
    created_at timestamptz NOT NULL,
    deleted_at timestamptz
);

CREATE INDEX projects_account_idx ON projects (account_id) WHERE deleted_at IS NULL;

CREATE TABLE outlets (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    name text NOT NULL,
    kind text NOT NULL,
    settings bytea NOT NULL, -- AES-GCM sealed JSON, see internal/secret
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz
);

CREATE INDEX outlets_project_idx ON outlets (project_id) WHERE deleted_at IS NULL;

CREATE TABLE expectations (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    name text NOT NULL,
    source text NOT NULL,
    probe jsonb,
    intake_token text UNIQUE,
    schedule_kind text NOT NULL,
    period_seconds bigint,
    slack_seconds bigint NOT NULL,
    cron_expr text,
    cron_timezone text NOT NULL DEFAULT 'UTC',
    status text NOT NULL,
    window_anchor timestamptz NOT NULL,
    deadline_at timestamptz NOT NULL,
    begun_at timestamptz,
    run_deadline_at timestamptz,
    last_ok_at timestamptz,
    probe_at timestamptz,
    version int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    CHECK ((source = 'intake' AND intake_token IS NOT NULL) OR (source = 'probe' AND probe IS NOT NULL))
);

CREATE INDEX expectations_project_idx ON expectations (project_id) WHERE deleted_at IS NULL;
CREATE INDEX expectations_due_idx ON expectations (deadline_at)
    WHERE status IN ('quiet', 'on_time') AND deleted_at IS NULL;
CREATE INDEX expectations_run_due_idx ON expectations (run_deadline_at)
    WHERE status IN ('quiet', 'on_time') AND run_deadline_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX expectations_probe_due_idx ON expectations (probe_at)
    WHERE source = 'probe' AND status <> 'held' AND deleted_at IS NULL;

CREATE TABLE expectation_outlets (
    expectation_id uuid NOT NULL REFERENCES expectations (id),
    outlet_id uuid NOT NULL REFERENCES outlets (id),
    PRIMARY KEY (expectation_id, outlet_id)
);

CREATE INDEX expectation_outlets_outlet_idx ON expectation_outlets (outlet_id);

CREATE TABLE signals (
    id uuid PRIMARY KEY,
    expectation_id uuid NOT NULL REFERENCES expectations (id),
    outcome text NOT NULL,
    note text NOT NULL DEFAULT '',
    received_at timestamptz NOT NULL
);

CREATE INDEX signals_expectation_idx ON signals (expectation_id, received_at DESC);

CREATE TABLE transitions (
    id uuid PRIMARY KEY,
    expectation_id uuid NOT NULL REFERENCES expectations (id),
    from_status text NOT NULL,
    to_status text NOT NULL,
    reason text NOT NULL,
    at timestamptz NOT NULL
);

CREATE INDEX transitions_expectation_idx ON transitions (expectation_id, at DESC);

CREATE TABLE deliveries (
    id uuid PRIMARY KEY,
    expectation_id uuid NOT NULL REFERENCES expectations (id),
    transition_id uuid NOT NULL REFERENCES transitions (id),
    project_id uuid NOT NULL REFERENCES projects (id),
    outlet_id uuid REFERENCES outlets (id),
    status text NOT NULL,
    attempt_count int NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT '',
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    sent_at timestamptz,
    claimed_at timestamptz
);

CREATE UNIQUE INDEX deliveries_transition_outlet_unique ON deliveries (transition_id, outlet_id);
CREATE UNIQUE INDEX deliveries_transition_unassigned_unique ON deliveries (transition_id) WHERE outlet_id IS NULL;
CREATE INDEX deliveries_pending_idx ON deliveries (created_at) WHERE status = 'pending';
CREATE INDEX deliveries_expectation_idx ON deliveries (expectation_id, created_at);
