package main

import "testing"

// TestResolveDevopsMode is the GRO-929 acceptance probe. Pins the
// (ENV, DEV_CONSOLE) decision matrix that gates /devops/* mounting.
//
// Three load-bearing properties:
//
//  1. With no env signals at all, the routes do NOT mount — production
//     deploys that forget to set DEV_CONSOLE or ENV are fail-closed.
//  2. ENV=production wins over DEV_CONSOLE — a misconfigured deploy
//     that sets both still ends up gated behind ops:read, never
//     unauthenticated.
//  3. DEV_CONSOLE=1 mounts unauthenticated only when ENV is not
//     "production".
//
// Fails pre-fix because pre-GRO-929 cmd/gateway/main.go always called
// devops.New(...).Mount(r) and webdevops.New(...).Mount(r) with no
// gate at all.
func TestResolveDevopsMode(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		devConsole string
		want       devopsMode
	}{
		{"empty environment fails closed", "", "", devopsModeOff},
		{"DEV_CONSOLE=0 fails closed", "", "0", devopsModeOff},
		{"DEV_CONSOLE=1 opens dev mount", "", "1", devopsModeDevOpen},
		{"DEV_CONSOLE=1 + ENV=staging stays dev open", "staging", "1", devopsModeDevOpen},
		{"ENV=production gates", "production", "", devopsModeProductionGated},
		{"ENV=production wins over DEV_CONSOLE=1", "production", "1", devopsModeProductionGated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveDevopsMode(c.env, c.devConsole)
			if got != c.want {
				t.Errorf("resolveDevopsMode(%q, %q) = %d, want %d",
					c.env, c.devConsole, got, c.want)
			}
		})
	}
}
