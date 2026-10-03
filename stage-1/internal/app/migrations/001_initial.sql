CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE users (
  id varchar(64) PRIMARY KEY,
  email varchar(320) NOT NULL UNIQUE,
  password_hash bytea NOT NULL,
  display_name text NOT NULL,
  created_at timestamptz NOT NULL
);
CREATE TABLE auth_sessions (
  id varchar(64) PRIMARY KEY,
  user_id varchar(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash bytea NOT NULL UNIQUE,
  created_at timestamptz NOT NULL
);
CREATE TABLE restaurants (
  id varchar(64) PRIMARY KEY,
  name text NOT NULL,
  timezone text NOT NULL,
  slot_minutes integer NOT NULL CHECK (slot_minutes > 0),
  reservation_duration_minutes integer NOT NULL CHECK (reservation_duration_minutes > 0),
  cancellation_cutoff_minutes integer NOT NULL CHECK (cancellation_cutoff_minutes >= 0),
  sort_order integer NOT NULL
);
CREATE TABLE restaurant_opening_hours (
  restaurant_id varchar(64) NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  weekday smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
  opens time NOT NULL,
  closes time NOT NULL,
  sort_order integer NOT NULL,
  PRIMARY KEY (restaurant_id, weekday),
  CHECK (closes > opens)
);
CREATE TABLE restaurant_tables (
  id varchar(64) NOT NULL,
  restaurant_id varchar(64) NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  label text NOT NULL,
  capacity integer NOT NULL CHECK (capacity > 0),
  sort_order integer NOT NULL,
  PRIMARY KEY (restaurant_id, id)
);
CREATE INDEX restaurant_tables_restaurant_idx ON restaurant_tables(restaurant_id, sort_order);
CREATE TABLE reservations (
  id varchar(64) PRIMARY KEY,
  reference varchar(12) NOT NULL UNIQUE CHECK (reference ~ '^[A-Z0-9]{6,12}$'),
  user_id varchar(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  restaurant_id varchar(64) NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  table_id varchar(64) NOT NULL,
  party_size integer NOT NULL CHECK (party_size > 0),
  status text NOT NULL CHECK (status IN ('confirmed', 'cancelled')),
  starts_at_local varchar(16) NOT NULL,
  starts_at timestamptz NOT NULL,
  ends_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  FOREIGN KEY (restaurant_id, table_id) REFERENCES restaurant_tables(restaurant_id, id)
);
CREATE INDEX reservations_user_start_idx ON reservations(user_id, starts_at DESC);
CREATE TABLE reservation_occupancies (
  reservation_id varchar(64) PRIMARY KEY REFERENCES reservations(id) ON DELETE CASCADE,
  restaurant_id varchar(64) NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  table_id varchar(64) NOT NULL,
  conflict_range tstzrange,
  FOREIGN KEY (restaurant_id, table_id) REFERENCES restaurant_tables(restaurant_id, id),
  CONSTRAINT reservation_occupancies_no_overlap EXCLUDE USING gist (
    restaurant_id WITH =, table_id WITH =, conflict_range WITH &&
  ) DEFERRABLE INITIALLY IMMEDIATE
);
CREATE TABLE idempotency_receipts (
  user_id varchar(64) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key varchar(255) NOT NULL,
  method text NOT NULL,
  path text NOT NULL,
  request_body jsonb NOT NULL,
  response_body jsonb NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (user_id, key, method, path)
);
