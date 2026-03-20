package service

import "testing"

func TestStageQueueDeduplicatesPendingTxn(t *testing.T) {
	q := newStageQueueWithWorkers(1, 1)
	if q == nil {
		t.Fatalf("queue is nil")
	}

	if ok := q.Enqueue("txn_1"); !ok {
		t.Fatalf("enqueue first txn failed")
	}
	if ok := q.Enqueue(" txn_1 "); !ok {
		t.Fatalf("enqueue duplicate txn should be treated as accepted")
	}
	if ok := q.Enqueue("txn_2"); ok {
		t.Fatalf("enqueue distinct txn should fail while queue is full")
	}

	first := <-q.workerQueues[0]
	if first.txnNo != "txn_1" {
		t.Fatalf("unexpected first txn: %s", first.txnNo)
	}

	// Duplicate should still be blocked until the current item is marked done.
	if ok := q.Enqueue("txn_1"); !ok {
		t.Fatalf("enqueue duplicate while processing should be treated as accepted")
	}
	select {
	case dup := <-q.workerQueues[0]:
		t.Fatalf("duplicate was re-enqueued unexpectedly: %s", dup.txnNo)
	default:
	}

	q.Done("txn_1")
	if ok := q.Enqueue("txn_1"); !ok {
		t.Fatalf("enqueue should succeed after done")
	}
	next := <-q.workerQueues[0]
	if next.txnNo != "txn_1" {
		t.Fatalf("unexpected re-enqueued txn: %s", next.txnNo)
	}
}

func TestStageQueueEnqueueWithRouteShardsToFixedWorker(t *testing.T) {
	q := newStageQueueWithWorkers(5, 20)
	if q == nil {
		t.Fatalf("queue is nil")
	}

	if ok := q.EnqueueWithRoute("txn_1", "acct_hot"); !ok {
		t.Fatalf("enqueue txn_1 failed")
	}
	if ok := q.EnqueueWithRoute("txn_2", "acct_hot"); !ok {
		t.Fatalf("enqueue txn_2 failed")
	}

	targetWorker := q.workerIndex("acct_hot", "")
	if targetWorker < 0 || targetWorker >= len(q.workerQueues) {
		t.Fatalf("invalid target worker index: %d", targetWorker)
	}

	for idx, ch := range q.workerQueues {
		got := len(ch)
		if idx == targetWorker && got != 2 {
			t.Fatalf("target worker queue depth mismatch: got=%d want=2", got)
		}
		if idx != targetWorker && got != 0 {
			t.Fatalf("unexpected item in other worker queue: worker=%d depth=%d", idx, got)
		}
	}
}

func TestStageQueueGlobalCapacityLimitAcrossShards(t *testing.T) {
	q := newStageQueueWithWorkers(5, 3)
	if q == nil {
		t.Fatalf("queue is nil")
	}

	if ok := q.EnqueueWithRoute("txn_1", "acct_a"); !ok {
		t.Fatalf("enqueue txn_1 failed")
	}
	if ok := q.EnqueueWithRoute("txn_2", "acct_a"); !ok {
		t.Fatalf("enqueue txn_2 failed")
	}
	if ok := q.EnqueueWithRoute("txn_3", "acct_b"); !ok {
		t.Fatalf("enqueue txn_3 failed")
	}
	if ok := q.EnqueueWithRoute("txn_4", "acct_c"); ok {
		t.Fatalf("enqueue should fail once global pending reaches capacity")
	}
}
