package migrations

import _ "embed"

// InitialSchema contains the idempotent MySQL bootstrap schema.
//
//go:embed 001_init.sql
var InitialSchema string
