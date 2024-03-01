// Copyright 2019 cruzbit developers

package plotthread

// InteractionQueue is an interface to a queue of interactions to be confirmed.
type InteractionQueue interface {
	// Add adds the interaction to the queue. Returns true if the interaction was added to the queue on this call.
	Add(id InteractionID, tx *Interaction) (bool, error)

	// AddBatch adds a batch of interactions to the queue (a plot has been disconnected.)
	// "height" is the plot thread height after this disconnection.
	AddBatch(ids []InteractionID, txs []*Interaction, height int64) error

	// RemoveBatch removes a batch of interactions from the queue (a plot has been connected.)
	// "height" is the plot thread height after this connection.
	// "more" indicates if more connections are coming.
	RemoveBatch(ids []InteractionID, height int64, more bool) error

	// Get returns interactions in the queue for the scriber.
	Get(limit int) []*Interaction

	// Exists returns true if the given interaction is in the queue.
	Exists(id InteractionID) bool

	// ExistsSigned returns true if the given interaction is in the queue and contains the given signature.
	ExistsSigned(id InteractionID, signature Signature) bool

	// Len returns the queue length.
	Len() int
}
