package github

import (
	"context"
	"testing"

	"github.com/actions/scaleset"
)

type fakeSession struct {
	msg *scaleset.RunnerScaleSetMessage
}

func (f *fakeSession) GetMessage(context.Context, int, int) (*scaleset.RunnerScaleSetMessage, error) {
	return f.msg, nil
}
func (f *fakeSession) DeleteMessage(context.Context, int) error              { return nil }
func (f *fakeSession) AcquireJobs(context.Context, []int64) ([]int64, error) { return nil, nil }
func (f *fakeSession) Session() scaleset.RunnerScaleSetSession {
	return scaleset.RunnerScaleSetSession{}
}

type availableRecorder struct{ got []string }

func (a *availableRecorder) HandleJobAvailable(_ context.Context, j *scaleset.JobAvailable) error {
	a.got = append(a.got, j.JobID)
	return nil
}

// GitHub sends a job's queue time only with its "available" message, which the
// scaleset listener consumes without handing it to the scaler.
func TestAvailableJobsReachTheHandler(t *testing.T) {
	msg := &scaleset.RunnerScaleSetMessage{JobAvailableMessages: []*scaleset.JobAvailable{
		{JobMessageBase: scaleset.JobMessageBase{JobID: "j1"}}, {JobMessageBase: scaleset.JobMessageBase{JobID: "j2"}}}}
	rec := &availableRecorder{}
	s := withAvailableJobs(&fakeSession{msg: msg}, rec)
	got, err := s.GetMessage(context.Background(), 0, 1)
	if err != nil || got != msg {
		t.Fatalf("message = %v, %v; want it passed through", got, err)
	}
	if len(rec.got) != 2 || rec.got[0] != "j1" || rec.got[1] != "j2" {
		t.Fatalf("handled %v, want [j1 j2]", rec.got)
	}
	if _, err := withAvailableJobs(&fakeSession{}, rec).GetMessage(context.Background(), 0, 1); err != nil {
		t.Fatalf("no message: %v", err)
	}
}
