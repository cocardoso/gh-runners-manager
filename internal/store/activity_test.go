package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// putJob stores a job with an explicit updated_at (UpsertJob stamps the current time).
func putJob(t *testing.T, s *Store, j Job) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE jobs SET updated_at = ? WHERE id = ?`, ms(j.UpdatedAt), j.ID); err != nil {
		t.Fatal(err)
	}
}

func TestScaleSetActivity(t *testing.T) {
	s := openTemp(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	h := func(n int) time.Time { return now.Add(-time.Duration(n) * time.Hour) }
	for _, j := range []Job{
		{ID: "a1", ScaleSet: "org", Repository: "o/one", Status: "completed", Result: "succeeded", FinishedAt: h(1), UpdatedAt: h(1)},
		{ID: "a2", ScaleSet: "org", Repository: "o/two", Status: "completed", Result: "failed", FinishedAt: h(2), UpdatedAt: h(2)},
		{ID: "a3", ScaleSet: "org", Repository: "o/one", Status: "completed", Result: "canceled", FinishedAt: h(3), UpdatedAt: h(3)},
		{ID: "a4", ScaleSet: "org", Repository: "o/old", Status: "completed", Result: "succeeded", FinishedAt: h(30), UpdatedAt: h(30)},
		{ID: "a5", ScaleSet: "org", Repository: "o/ancient", Status: "completed", Result: "succeeded", FinishedAt: h(24 * 10), UpdatedAt: h(24 * 10)},
		{ID: "a6", ScaleSet: "org", Repository: "o/three", Status: "running", UpdatedAt: h(0)},
		{ID: "a7", ScaleSet: "org", Status: "assigned", UpdatedAt: h(0).Add(-time.Minute)},
		{ID: "b1", ScaleSet: "repo", Repository: "o/r", Status: "completed", Result: "succeeded", FinishedAt: h(5), UpdatedAt: h(5)},
	} {
		putJob(t, s, j)
	}

	got, err := s.ScaleSetActivity(context.Background(), h(24), h(24*7))
	if err != nil {
		t.Fatal(err)
	}
	org := got["org"]
	if org.Running != 2 || org.Jobs != 3 || org.Succeeded != 1 || org.Failed != 1 {
		t.Fatalf("org counts = %+v", org)
	}
	if org.LastJob.ID != "a6" || org.LastJob.Status != "running" || org.LastJob.Repository != "o/three" {
		t.Fatalf("org last job = %+v", org.LastJob)
	}
	if want := []string{"o/old", "o/one", "o/three", "o/two"}; !reflect.DeepEqual(org.Repositories, want) {
		t.Fatalf("org repositories = %v, want %v", org.Repositories, want)
	}
	repo := got["repo"]
	if repo.Running != 0 || repo.Jobs != 1 || repo.Succeeded != 1 || repo.LastJob.ID != "b1" || !repo.LastJob.FinishedAt.Equal(h(5)) {
		t.Fatalf("repo = %+v", repo)
	}
	if _, ok := got["none"]; ok {
		t.Fatal("a scale set without jobs must be absent")
	}
}
