package main

import "testing"

// TestResolveSecretBackend is the GRO-930 acceptance probe. Pins the
// (ENV, SECRET_BACKEND, GCP_PROJECT_ID) production-fail-closed truth
// table for the gateway's secrets backend selection.
//
// Three load-bearing properties:
//
//  1. ENV=production with SECRET_BACKEND unset/pgx must fatal —
//     plaintext webhook secrets cannot ship.
//  2. ENV=production with SECRET_BACKEND=sm but no GCP_PROJECT_ID
//     must fatal — partial misconfig is just as risky.
//  3. Non-production envs preserve the dev fallback to pgx so a
//     local boot without GCP creds still works.
//
// Fails pre-fix because pre-GRO-930 production missing SECRET_BACKEND
// silently fell through to PgxResolver — the only opt-in guard was
// the easily-forgotten SECRET_BACKEND_REQUIRE_SM=1 flag.
func TestResolveSecretBackend(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		backend    string
		gcpProject string
		want       secretBackendDecision
	}{
		// Dev/CI happy paths
		{"dev empty backend → pgx", "", "", "", secretBackendOK_Pgx},
		{"dev backend=pgx → pgx", "", "pgx", "", secretBackendOK_Pgx},
		{"dev backend=sm + project → sm", "", "sm", "growdirect-dev", secretBackendOK_SM},
		{"dev backend=sm no project → pgx fallback", "", "sm", "", secretBackendOK_Pgx},
		{"staging backend=sm + project → sm", "staging", "sm", "growdirect-staging", secretBackendOK_SM},
		{"staging missing project → pgx fallback (legacy behavior)", "staging", "sm", "", secretBackendOK_Pgx},

		// Production fail-closed
		{"production empty backend → fatal", "production", "", "", secretBackendFatalProductionMissingSM},
		{"production backend=pgx → fatal", "production", "pgx", "growdirect-prod", secretBackendFatalProductionMissingSM},
		{"production backend=other → fatal", "production", "vault", "growdirect-prod", secretBackendFatalProductionMissingSM},
		{"production backend=sm no project → fatal", "production", "sm", "", secretBackendFatalProductionMissingProject},
		{"production backend=sm + project → ok", "production", "sm", "growdirect-prod", secretBackendOK_SM},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveSecretBackend(c.env, c.backend, c.gcpProject)
			if got != c.want {
				t.Errorf("resolveSecretBackend(env=%q, backend=%q, project=%q) = %d, want %d",
					c.env, c.backend, c.gcpProject, got, c.want)
			}
		})
	}
}
