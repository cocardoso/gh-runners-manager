package github

import (
	"context"
	"log/slog"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

// AvailableJobHandler receives the "job available" and "job assigned" messages, which
// carry a job's queue time (started and completed messages do not). A listener.Scaler
// may implement it.
type AvailableJobHandler interface {
	HandleJobAvailable(ctx context.Context, job *scaleset.JobAvailable) error
}

// availableJobs hands the "job available" messages to a handler before the
// listener acquires them; the scaleset listener does not pass them to the scaler.
type availableJobs struct {
	listener.Client
	h      AvailableJobHandler
	logger *slog.Logger
}

func withAvailableJobs(c listener.Client, h AvailableJobHandler, logger *slog.Logger) listener.Client {
	return &availableJobs{Client: c, h: h, logger: logger}
}

func (a *availableJobs) GetMessage(ctx context.Context, lastMessageID, maxCapacity int) (*scaleset.RunnerScaleSetMessage, error) {
	msg, err := a.Client.GetMessage(ctx, lastMessageID, maxCapacity)
	if err != nil || msg == nil {
		return msg, err
	}
	jobs := append([]*scaleset.JobAvailable{}, msg.JobAvailableMessages...)
	for _, j := range msg.JobAssignedMessages {
		jobs = append(jobs, &scaleset.JobAvailable{JobMessageBase: j.JobMessageBase})
	}
	for _, j := range jobs {
		a.logger.Debug("job message", "type", j.MessageType, "job_id", j.JobID, "queue_time", j.QueueTime,
			"scale_set_assign_time", j.ScaleSetAssignTime, "runner_assign_time", j.RunnerAssignTime)
		// Recording the queue time is best effort: it never blocks acquiring the job.
		_ = a.h.HandleJobAvailable(context.WithoutCancel(ctx), j)
	}
	for _, j := range msg.JobStartedMessages {
		a.logger.Debug("job message", "type", j.MessageType, "job_id", j.JobID, "queue_time", j.QueueTime,
			"scale_set_assign_time", j.ScaleSetAssignTime, "runner_assign_time", j.RunnerAssignTime)
	}
	return msg, nil
}
