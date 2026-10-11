package clock

import (
	"testing"
	"time"
)

func TestFake(t *testing.T) {
	start := time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC)
	f := NewFake(start)

	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}

	f.Advance(90 * time.Minute)
	if got, want := f.Now(), start.Add(90*time.Minute); !got.Equal(want) {
		t.Fatalf("after Advance, Now() = %v, want %v", got, want)
	}

	reset := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	f.Set(reset)
	if got := f.Now(); !got.Equal(reset) {
		t.Fatalf("after Set, Now() = %v, want %v", got, reset)
	}
}

func TestFakeNormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("test", 5*3600)
	f := NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, zone))

	if got := f.Now().Location(); got != time.UTC {
		t.Fatalf("Now().Location() = %v, want UTC", got)
	}
}

func TestRealIsUTC(t *testing.T) {
	if got := (Real{}).Now().Location(); got != time.UTC {
		t.Fatalf("Real.Now().Location() = %v, want UTC", got)
	}
}
