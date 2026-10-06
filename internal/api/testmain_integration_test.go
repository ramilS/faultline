//go:build integration

package api_test

import (
	"testing"

	"github.com/ramilS/faultline/internal/testdatabase"
)

// TestMain hands the run to testdatabase so the TimescaleDB container shared
// by every integration package is removed once the last test binary exits.
func TestMain(m *testing.M) {
	testdatabase.RunTests(m)
}
