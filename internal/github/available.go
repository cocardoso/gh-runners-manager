package github

import (
	"context"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

// AvailableJobHandler receives the "job available" messages, the only ones that
// carry a job's queue time. A listener.Scaler may implement it.
type AvailableJobHandler interface {
	HandleJobAvailable(ctx context.Context, job *scaleset.JobAvailable) error
}

// availableJobs hands the "job available" messages to a handler before the
// listener acquires them; the scaleset listener does not pass them to the scaler.
type availableJobs struct {
	listener.Client
	h AvailableJobHandler
}

func withAvailableJobs(c listener.Client, h AvailableJobHandler) listener.Client {
	return &availableJobs{Client: c, h: h}
}

func (a *availableJobs) GetMessage(ctx context.Context, lastMessageID, maxCapacity int) (*scaleset.RunnerScaleSetMessage, error) {
	msg, err := a.Client.GetMessage(ctx, lastMessageID, maxCapacity)
	if err != nil || msg == nil {
		return msg, err
	}
	for _, j := range msg.JobAvailableMessages {
		// Recording the queue time is best effort: it never blocks acquiring the job.
		_ = a.h.HandleJobAvailable(context.WithoutCancel(ctx), j)
	}
	return msg, nil
}
