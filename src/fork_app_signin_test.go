package main

import "testing"

func TestTheInstallPlanSaysHowEachImageSignsIn(t *testing.T) {
	got := signInOfImages([]string{"postgres:18", "jellyfin/jellyfin:10.11", "nginx:1", "registry.example.org/team/app:1"})
	if pg, ok := got["postgres:18"]; !ok || pg.Login == nil || pg.Login.Password == nil || len(pg.Login.Accounts) == 0 {
		t.Fatalf("postgres: %+v", got["postgres:18"])
	}
	if j, ok := got["jellyfin/jellyfin:10.11"]; !ok || j.FirstRun == nil || j.Login != nil {
		t.Fatalf("jellyfin: %+v", got["jellyfin/jellyfin:10.11"])
	}
	if _, ok := got["nginx:1"]; ok {
		t.Fatal("nginx has no sign-in")
	}
	if _, ok := got["registry.example.org/team/app:1"]; ok {
		t.Fatal("an unknown image has no entry: the panel guesses")
	}
}
