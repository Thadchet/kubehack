package main

import (
	"slices"
	"testing"
)

func TestEachPlatformAllowsOnlyItsOwnOrigin(t *testing.T) {
	for goos, want := range map[string]string{
		"darwin":  "wails://wails",
		"windows": "http://wails.localhost",
	} {
		if got := appOrigins(goos); !slices.Equal(got, []string{want}) {
			t.Errorf("%s: origins = %v", goos, got)
		}
	}
}
