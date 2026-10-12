package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func sampleHabit(id string) Habit {
	return Habit{
		ID: id, Name: "Walk after lunch", Why: "clears my head", Anchor: "After I finish lunch",
		Behavior: "Walk for 10 minutes", TinyVersion: "Put on shoes and step outside",
		Cadence: "daily", WindowStart: "12:30", WindowEnd: "15:00", CreatedAt: t0,
	}
}

func TestHabitCRUD(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if _, err := db.GetHabit(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetHabit(missing) = %v", err)
	}

	h := sampleHabit("h1")
	if err := db.AddHabit(ctx, h); err != nil {
		t.Fatal(err)
	}
	h2 := sampleHabit("h2")
	h2.CreatedAt = t0.Add(time.Hour)
	if err := db.AddHabit(ctx, h2); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetHabit(ctx, "h1")
	if err != nil || got.State != HabitActive || got.TinyVersion != h.TinyVersion || !got.CreatedAt.Equal(t0) {
		t.Fatalf("GetHabit = %+v, %v", got, err)
	}

	h.Name, h.State = "Walk", HabitPaused
	if err := db.UpdateHabit(ctx, h); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetHabit(ctx, "h1"); got.Name != "Walk" || got.State != HabitPaused {
		t.Errorf("after update = %+v", got)
	}
	if err := db.UpdateHabit(ctx, sampleHabit("ghost")); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateHabit(missing) = %v", err)
	}

	h2.State = HabitRetired
	_ = db.UpdateHabit(ctx, h2)
	list, _ := db.ListHabits(ctx, false)
	if len(list) != 1 || list[0].ID != "h1" {
		t.Errorf("ListHabits(false) = %+v, want the retired habit hidden", list)
	}
	if list, _ := db.ListHabits(ctx, true); len(list) != 2 || list[0].ID != "h1" {
		t.Errorf("ListHabits(true) = %+v", list)
	}
}

func TestHabitStateConstraint(t *testing.T) {
	db := openTest(t)
	h := sampleHabit("h1")
	h.State = "zombie"
	if err := db.AddHabit(context.Background(), h); err == nil {
		t.Error("invalid state accepted")
	}
}

func TestHabitLogs(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	_ = db.AddHabit(ctx, sampleHabit("h1"))

	for day, status := range map[string]string{
		"2026-10-08": LogDone, "2026-10-09": LogTiny, "2026-10-10": LogSkipped, "2026-10-11": LogDone,
	} {
		if err := db.SetHabitLog(ctx, HabitLog{HabitID: "h1", Day: day, Status: status, TS: t0}); err != nil {
			t.Fatal(err)
		}
	}
	// Re-logging a day replaces it.
	if err := db.SetHabitLog(ctx, HabitLog{HabitID: "h1", Day: "2026-10-10", Status: LogTiny, Note: "stretched", TS: t0}); err != nil {
		t.Fatal(err)
	}

	logs, err := db.HabitLogs(ctx, "h1", "2026-10-09", "2026-10-11")
	if err != nil || len(logs) != 3 || logs[0].Day != "2026-10-09" || logs[1].Status != LogTiny || logs[1].Note != "stretched" {
		t.Fatalf("HabitLogs = %+v, %v", logs, err)
	}

	n, err := db.ShowingUpDays(ctx, "h1")
	if err != nil || n != 4 {
		t.Errorf("ShowingUpDays = %d, %v; want 4 (done and tiny both count)", n, err)
	}

	if err := db.SetHabitLog(ctx, HabitLog{HabitID: "h1", Day: "2026-10-12", Status: "meh", TS: t0}); err == nil {
		t.Error("invalid status accepted")
	}
	if err := db.SetHabitLog(ctx, HabitLog{HabitID: "ghost", Day: "2026-10-12", Status: LogDone, TS: t0}); err == nil {
		t.Error("log for an unknown habit accepted (foreign keys should be on)")
	}
}

func TestDeletingAHabitRemovesItsLogs(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	_ = db.AddHabit(ctx, sampleHabit("h1"))
	_ = db.SetHabitLog(ctx, HabitLog{HabitID: "h1", Day: "2026-10-11", Status: LogDone, TS: t0})

	if _, err := db.Write().ExecContext(ctx, "DELETE FROM habits WHERE id = 'h1'"); err != nil {
		t.Fatal(err)
	}
	if logs, _ := db.HabitLogs(ctx, "h1", "2026-01-01", "2026-12-31"); len(logs) != 0 {
		t.Errorf("logs survived their habit: %+v", logs)
	}
}

func TestSleepEventsAndActivity(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if _, ok, _ := db.LastActivity(ctx, t0); ok {
		t.Error("LastActivity on an empty table reported an event")
	}
	for i, k := range []string{SleepActivity, SleepBedtime, SleepActivity, SleepWake} {
		if err := db.AddSleepEvent(ctx, SleepEvent{TS: t0.Add(time.Duration(i) * time.Hour), Kind: k}); err != nil {
			t.Fatal(err)
		}
	}

	evs, err := db.SleepEvents(ctx, t0.Add(time.Hour), t0.Add(3*time.Hour))
	if err != nil || len(evs) != 2 || evs[0].Kind != SleepBedtime || evs[0].Source != "chat" {
		t.Fatalf("SleepEvents = %+v, %v; want [from, to)", evs, err)
	}

	last, ok, err := db.LastActivity(ctx, t0.Add(10*time.Hour))
	if err != nil || !ok || !last.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("LastActivity = %v, %v, %v; want the activity at +2h (not the wake event)", last, ok, err)
	}
	if err := db.AddSleepEvent(ctx, SleepEvent{TS: t0, Kind: "nap"}); err == nil {
		t.Error("invalid sleep event kind accepted")
	}
}

func TestSleepNights(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	for i, day := range []string{"2026-10-09", "2026-10-10", "2026-10-11"} {
		n := SleepNight{
			Day: day, Onset: t0.Add(time.Duration(i) * 24 * time.Hour), Wake: t0.Add(time.Duration(i)*24*time.Hour + 7*time.Hour),
			Confidence: ConfInferred,
		}
		if err := db.SetSleepNight(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	// Confirming a night replaces its confidence.
	if err := db.SetSleepNight(ctx, SleepNight{Day: "2026-10-11", Onset: t0, Wake: t0.Add(8 * time.Hour), Confidence: ConfConfirmed, UserConfirmed: true}); err != nil {
		t.Fatal(err)
	}

	nights, err := db.SleepNights(ctx, "2026-10-11", 2)
	if err != nil || len(nights) != 2 || nights[0].Day != "2026-10-11" || nights[1].Day != "2026-10-10" {
		t.Fatalf("SleepNights = %+v, %v; want newest first, limited", nights, err)
	}
	if !nights[0].UserConfirmed || nights[0].Confidence != ConfConfirmed {
		t.Errorf("confirmation lost: %+v", nights[0])
	}

	if _, err := db.GetSleepNight(ctx, "2026-10-12"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSleepNight(future) = %v, want ErrNotFound", err)
	}
	if n, err := db.GetSleepNight(ctx, "2026-10-10"); err != nil || n.Day != "2026-10-10" {
		t.Errorf("GetSleepNight = %+v, %v", n, err)
	}
	if err := db.SetSleepNight(ctx, SleepNight{Day: "2026-10-13", Onset: t0, Wake: t0, Confidence: "guess"}); err == nil {
		t.Error("invalid confidence accepted")
	}
}

func TestNotificationsAndResponses(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	for i, kind := range []string{"morning", "habit:h1", "evening"} {
		if _, err := db.AddNotification(ctx, Notification{TS: t0.Add(time.Duration(i) * time.Hour), Kind: kind, Variant: "a"}); err != nil {
			t.Fatal(err)
		}
	}

	// Activity 30 minutes after the second message answers the second one only.
	ok, err := db.MarkResponded(ctx, t0.Add(90*time.Minute), 3*time.Hour)
	if err != nil || !ok {
		t.Fatalf("MarkResponded = %v, %v", ok, err)
	}
	// A second activity is credited to the earlier unanswered message.
	if ok, _ := db.MarkResponded(ctx, t0.Add(100*time.Minute), 3*time.Hour); !ok {
		t.Error("the older unanswered message was not credited")
	}
	// Nothing left in the window.
	if ok, _ := db.MarkResponded(ctx, t0.Add(110*time.Minute), 3*time.Hour); ok {
		t.Error("an already-answered message was credited again")
	}
	// Activity before any message, or long after, answers nothing.
	if ok, _ := db.MarkResponded(ctx, t0.Add(-time.Hour), 3*time.Hour); ok {
		t.Error("activity before a message answered it")
	}
	if ok, _ := db.MarkResponded(ctx, t0.Add(10*time.Hour), time.Hour); ok {
		t.Error("activity outside the window answered a message")
	}

	got, err := db.Notifications(ctx, t0)
	if err != nil || len(got) != 3 {
		t.Fatalf("Notifications = %+v, %v", got, err)
	}
	if got[0].RespondedAt.IsZero() || got[1].RespondedAt.IsZero() || !got[2].RespondedAt.IsZero() {
		t.Errorf("response marks = %v %v %v", got[0].RespondedAt, got[1].RespondedAt, got[2].RespondedAt)
	}
	if since, _ := db.Notifications(ctx, t0.Add(90*time.Minute)); len(since) != 1 || since[0].Kind != "evening" {
		t.Errorf("Notifications(since) = %+v", since)
	}
}
