package service

import "testing"

func TestStageQueueDeduplicatesPendingTxn(t *testing.T) {
	q := newStageQueue(1)
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

	first := <-q.ch
	if first.txnNo != "txn_1" {
		t.Fatalf("unexpected first txn: %s", first.txnNo)
	}

	// Duplicate should still be blocked until the current item is marked done.
	if ok := q.Enqueue("txn_1"); !ok {
		t.Fatalf("enqueue duplicate while processing should be treated as accepted")
	}
	select {
	case dup := <-q.ch:
		t.Fatalf("duplicate was re-enqueued unexpectedly: %s", dup.txnNo)
	default:
	}

	q.Done("txn_1")
	if ok := q.Enqueue("txn_1"); !ok {
		t.Fatalf("enqueue should succeed after done")
	}
	next := <-q.ch
	if next.txnNo != "txn_1" {
		t.Fatalf("unexpected re-enqueued txn: %s", next.txnNo)
	}
}
