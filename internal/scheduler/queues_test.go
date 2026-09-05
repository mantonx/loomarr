package scheduler

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

// The queue split (§18.1): a job's ceiling must bound only the jobs that share its queue.
//
// ⚠ These are pure — no database, no River client — because what they pin is the ROUTING RULE.
// The behavioural proof that a long job cannot starve a cheap one lives in
// `internal/app/river_queues_test.go`, against real River and real SQLite.

func queueTestJob(name string, timeout time.Duration) Job {
	return Job{
		Name: name, Group: GroupSystem, Title: name, Description: "queue test job " + name + ".",
		DefaultCron: everyMinute, Timeout: timeout,
		Run: func(context.Context) error { return nil },
	}
}

func TestQueueFor_TheCeilingChoosesTheQueue(t *testing.T) {
	if got := queueFor(queueTestJob("quick", 0)); got != river.QueueDefault {
		t.Errorf("a job with no declared ceiling = %q, want %q", got, river.QueueDefault)
	}
	if got := queueFor(queueTestJob("bounded", 10*time.Second)); got != river.QueueDefault {
		t.Errorf("a job bounded below River's default ceiling = %q, want %q", got, river.QueueDefault)
	}
	if got := queueFor(queueTestJob("slow", LongJobTimeout)); got != longQueue {
		t.Errorf("a job above River's default ceiling = %q, want %q", got, longQueue)
	}
}

// ⚠ **THE invariant that makes a DERIVED queue safe.** A hand-set `Job.Queue` field could name a
// queue with no producer, and the job would then never run — silently, forever, with no error
// anywhere. Because the queue SET and the queue ROUTING both come from `queueFor`, that state is
// unreachable; this is what stops someone reintroducing it.
func TestRiverQueues_EveryJobRoutesToAQueueThatHasAProducer(t *testing.T) {
	reg := NewRegistry().
		Add(queueTestJob("quick", 0)).
		Add(queueTestJob("slow", LongJobTimeout))
	s := New(newFakeStore(), reg, nil, time.Now, testLog())

	queues := s.riverQueues(nil)

	for _, j := range s.jobs {
		if _, ok := queues[queueFor(j)]; !ok {
			t.Errorf("job %q routes to queue %q, which has no producer — it would never run",
				j.Name, queueFor(j))
		}
	}
	if len(queues) != 2 {
		t.Fatalf("queues = %v, want exactly a default and a long one", queues)
	}
	// ⚠ `long` is 1 on EVERY backend, and not for the SQLite pool's sake: ffmpeg competes with
	// playout for the GPU, so a media worker POOL would turn a catalog import into a live-channel
	// outage. Widening this is a §10 decision, not a tuning knob.
	if n := queues[longQueue].MaxWorkers; n != 1 {
		t.Errorf("long queue MaxWorkers = %d, want 1", n)
	}
}

// A registry with no long-running job must not mint an idle producer.
func TestRiverQueues_NoLongJobsMeansNoLongQueue(t *testing.T) {
	s := New(newFakeStore(), NewRegistry().
		Add(queueTestJob("quick", 0)).
		Add(queueTestJob("bounded", 10*time.Second)), nil, time.Now, testLog())

	queues := s.riverQueues(nil)

	if _, ok := queues[longQueue]; ok {
		t.Errorf("queues = %v, want no `long` producer when no ceiling exceeds River's default", queues)
	}
}

var _ = slog.LevelInfo // keep the import honest if testLog changes shape
