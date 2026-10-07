// Package ingest receives events, logs and metrics from ghrm-agent (spec §4.3).
// This file is the wire protocol, shared with the agent.
package ingest

import "time"

// FramesPath is the endpoint agents POST NDJSON frames to.
const FramesPath = "/ingest/v1/frames"

// MaxBodyBytes bounds one request body.
const MaxBodyBytes = 4 << 20

// Frame types.
const (
	TypeLog    = "log"
	TypeEvent  = "event"
	TypeMetric = "metric"
)

// Agent event names. Events share the "agent" stream's sequence numbers, so a
// replayed event is recognised and delivered once.
const (
	EventHello          = "hello"
	EventRunnerStarted  = "runner_started"
	EventRunnerOnline   = "runner_online"
	EventJobStarted     = "job_started"
	EventJobFinished    = "job_finished"
	EventRunnerExited   = "runner_exited"
	EventFramesDropped  = "frames_dropped"
	EventFramesRejected = "frames_rejected"
	EventShutdown       = "shutdown"
)

// AgentStreams are the log streams an agent may write.
var AgentStreams = []string{"agent", "runner", "job", "build", "selftest"}

// Frame is one NDJSON line of an ingest request.
type Frame struct {
	Type     string         `json:"type"`
	Stream   string         `json:"stream,omitempty"`
	Seq      int64          `json:"seq"`
	Time     time.Time      `json:"time"`
	Text     string         `json:"text,omitempty"`
	Name     string         `json:"name,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	CPUUsec  int64          `json:"cpu_usec,omitempty"`
	MemBytes int64          `json:"mem_bytes,omitempty"`
}

// Bootstrap variables injected into every environment (read from /proc/1/environ).
const (
	EnvJITConfig   = "GHRM_JITCONFIG"
	EnvEnvironment = "GHRM_ENVIRONMENT_ID"
	EnvURL         = "GHRM_INGEST_URL"
	EnvToken       = "GHRM_INGEST_TOKEN"
	EnvFingerprint = "GHRM_INGEST_FINGERPRINT"
)
