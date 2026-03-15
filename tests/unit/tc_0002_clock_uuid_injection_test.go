package unit

import (
	"testing"
	"time"

	clockpkg "github.com/xmz-ai/coin/internal/platform/clock"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
)

func TestTC0002ClockAndUUIDInjection(t *testing.T) {
	fixed := time.Date(2026, 3, 13, 9, 0, 0, 0, time.UTC)

	clk := clockpkg.NewFixed(fixed)
	if got := clk.NowUTC(); !got.Equal(fixed) {
		t.Fatalf("expected fixed time %v, got %v", fixed, got)
	}

	uuidProvider := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2001",
		"01956f4e-ae33-75cd-90a2-4c6f9d8b3001",
	})

	first, err := uuidProvider.NewUUIDv7()
	if err != nil {
		t.Fatalf("expected first uuid without error, got %v", err)
	}
	if first != "01956f4e-9d22-73bc-8e11-3f5e9c7a2001" {
		t.Fatalf("unexpected first uuid: %s", first)
	}

	second, err := uuidProvider.NewUUIDv7()
	if err != nil {
		t.Fatalf("expected second uuid without error, got %v", err)
	}
	if second != "01956f4e-ae33-75cd-90a2-4c6f9d8b3001" {
		t.Fatalf("unexpected second uuid: %s", second)
	}
}
