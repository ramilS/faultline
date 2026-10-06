package dashboard

import (
	"testing"
	"testing/fstest"
)

func TestIndexFallsBackWhenDashboardBuildIsAbsent(t *testing.T) {
	files := fstest.MapFS{
		"fallback.html": &fstest.MapFile{Data: []byte("fallback")},
	}

	got, err := index(files)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if string(got) != "fallback" {
		t.Fatalf("index = %q, want fallback", got)
	}
}
