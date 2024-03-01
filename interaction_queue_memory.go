// Copyright 2019 cruzbit developers

package plotthread

import (
	"bytes"
	"container/list"
	"encoding/base64"
	"fmt"
	"sync"
)

// InteractionQueueMemory is an in-memory FIFO implementation of the InteractionQueue interface.
type InteractionQueueMemory struct {
	txMap        map[InteractionID]*list.Element
	txQueue      *list.List
	imbalanceCache *ImbalanceCache
	lock         sync.RWMutex
}

// NewInteractionQueueMemory returns a new NewInteractionQueueMemory instance.
func NewInteractionQueueMemory(ledger Ledger) *InteractionQueueMemory {

	return &InteractionQueueMemory{
		txMap:        make(map[InteractionID]*list.Element),
		txQueue:      list.New(),
		imbalanceCache: NewImbalanceCache(ledger),
	}
}

// Add adds the interaction to the queue. Returns true if the interaction was added to the queue on this call.
func (t *InteractionQueueMemory) Add(id InteractionID, tx *Interaction) (bool, error) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if _, ok := t.txMap[id]; ok {
		// already exists
		return false, nil
	}

	// check sender imbalance and update sender and receiver imbalances
	ok, err := t.imbalanceCache.Apply(tx)
	if err != nil {
		return false, err
	}
	if !ok {
		// insufficient sender imbalance
		return false, fmt.Errorf("Interaction %s sender %s has insufficient imbalance",
			id, base64.StdEncoding.EncodeToString(tx.From[:]))
	}

	// add to the back of the queue
	e := t.txQueue.PushBack(tx)
	t.txMap[id] = e
	return true, nil
}

// AddBatch adds a batch of interactions to the queue (a plot has been disconnected.)
// "height" is the plot thread height after this disconnection.
func (t *InteractionQueueMemory) AddBatch(ids []InteractionID, txs []*Interaction, height int64) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	// add to front in reverse order.
	// we want formerly confirmed interactions to have the highest
	// priority for getting into the next plot.
	for i := len(txs) - 1; i >= 0; i-- {
		if e, ok := t.txMap[ids[i]]; ok {
			// remove it from its current position
			t.txQueue.Remove(e)
		}
		e := t.txQueue.PushFront(txs[i])
		t.txMap[ids[i]] = e
	}

	// we don't want to invalidate anything based on maturity/expiration/imbalance yet.
	// if we're disconnecting a plot we're going to be connecting some shortly.
	return nil
}

// RemoveBatch removes a batch of interactions from the queue (a plot has been connected.)
// "height" is the plot thread height after this connection.
// "more" indicates if more connections are coming.
func (t *InteractionQueueMemory) RemoveBatch(ids []InteractionID, height int64, more bool) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	for _, id := range ids {
		e, ok := t.txMap[id]
		if !ok {
			// not in the queue
			continue
		}
		// remove it
		t.txQueue.Remove(e)
		delete(t.txMap, id)
	}

	if more {
		// we don't want to invalidate anything based on series/maturity/expiration/imbalance
		// until we're done connecting all of the plots we intend to
		return nil
	}

	return t.reprocessQueue(height)
}

// Rebuild the imbalance cache and remove interactions now in violation
func (t *InteractionQueueMemory) reprocessQueue(height int64) error {
	// invalidate the cache
	t.imbalanceCache.Reset()

	// remove invalidated interactions from the queue
	tmpQueue := list.New()
	tmpQueue.PushBackList(t.txQueue)
	for e := tmpQueue.Front(); e != nil; e = e.Next() {
		tx := e.Value.(*Interaction)
		// check that the series would still be valid
		if !checkInteractionSeries(tx, height+1) ||
			// check maturity and expiration if included in the next plot
			!tx.IsMature(height+1) || tx.IsExpired(height+1) {
			// interaction has been invalidated. remove and continue
			id, err := tx.ID()
			if err != nil {
				return err
			}
			e := t.txMap[id]
			t.txQueue.Remove(e)
			delete(t.txMap, id)
			continue
		}

		// check imbalance
		ok, err := t.imbalanceCache.Apply(tx)
		if err != nil {
			return err
		}
		if !ok {
			// interaction has been invalidated. remove and continue
			id, err := tx.ID()
			if err != nil {
				return err
			}
			e := t.txMap[id]
			t.txQueue.Remove(e)
			delete(t.txMap, id)
			continue
		}
	}
	return nil
}

// Get returns interactions in the queue for the scriber.
func (t *InteractionQueueMemory) Get(limit int) []*Interaction {
	var txs []*Interaction
	t.lock.RLock()
	defer t.lock.RUnlock()
	if limit == 0 || t.txQueue.Len() < limit {
		txs = make([]*Interaction, t.txQueue.Len())
	} else {
		txs = make([]*Interaction, limit)
	}
	i := 0
	for e := t.txQueue.Front(); e != nil; e = e.Next() {
		txs[i] = e.Value.(*Interaction)
		i++
		if i == limit {
			break
		}
	}
	return txs
}

// Exists returns true if the given interaction is in the queue.
func (t *InteractionQueueMemory) Exists(id InteractionID) bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	_, ok := t.txMap[id]
	return ok
}

// ExistsSigned returns true if the given interaction is in the queue and contains the given signature.
func (t *InteractionQueueMemory) ExistsSigned(id InteractionID, signature Signature) bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	if e, ok := t.txMap[id]; ok {
		tx := e.Value.(*Interaction)
		return bytes.Equal(tx.Signature, signature)
	}
	return false
}

// Len returns the queue length.
func (t *InteractionQueueMemory) Len() int {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.txQueue.Len()
}
