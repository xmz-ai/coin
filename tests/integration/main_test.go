package integration

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	stopAutoPostgresContainer()
	os.Exit(code)
}
