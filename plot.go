// Copyright 2019 cruzbit developers

package plotthread

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math/big"
	"math/rand"
	"time"

	"golang.org/x/crypto/sha3"
)

// Plot represents a plot in the plot thread. It has a header and a list of interactions.
// As plots are connected their interactions affect the underlying ledger.
type Plot struct {
	Header       *PlotHeader   `json:"header"`
	Interactions []*Interaction `json:"interactions"`
	hasher       hash.Hash      // hash state used by scriber. not marshaled
}

// PlotHeader contains data used to determine plot validity and its place in the plot thread.
type PlotHeader struct {
	Previous         PlotID            `json:"previous"`
	HashListRoot     InteractionID      `json:"hash_list_root"`
	Time             int64              `json:"time"`
	Target           PlotID            `json:"target"`
	ThreadWork        PlotID            `json:"thread_work"` // total cumulative thread work
	Nonce            int64              `json:"nonce"`      // not used for crypto
	Height           int64              `json:"height"`
	InteractionCount int32              `json:"interaction_count"`
	hasher           *PlotHeaderHasher // used to speed up scribing. not marshaled
}

// PlotID is a plot's unique identifier.
type PlotID [32]byte // SHA3-256 hash

// NewPlot creates and returns a new Plot to be scribed.
func NewPlot(previous PlotID, height int64, target, threadWork PlotID, interactions []*Interaction) (
	*Plot, error) {

	// enforce the hard cap interaction limit
	if len(interactions) > MAX_INTERACTIONS_PER_PLOT {
		return nil, fmt.Errorf("Interaction list size exceeds limit per plot")
	}

	// compute the hash list root
	hasher := sha3.New256()
	hashListRoot, err := computeHashListRoot(hasher, interactions)
	if err != nil {
		return nil, err
	}

	// create the header and plot
	return &Plot{
		Header: &PlotHeader{
			Previous:         previous,
			HashListRoot:     hashListRoot,
			Time:             time.Now().Unix(), // just use the system time
			Target:           target,
			ThreadWork:        computeThreadWork(target, threadWork),
			Nonce:            rand.Int63n(MAX_NUMBER),
			Height:           height,
			InteractionCount: int32(len(interactions)),
		},
		Interactions: interactions,
		hasher:       hasher, // save this to use while scribing
	}, nil
}

// ID computes an ID for a given plot.
func (b Plot) ID() (PlotID, error) {
	return b.Header.ID()
}

// CheckPOW verifies the plot's proof-of-work satisfies the declared target.
func (b Plot) CheckPOW(id PlotID) bool {
	return id.GetBigInt().Cmp(b.Header.Target.GetBigInt()) <= 0
}

// AddInteraction adds a new interaction to the plot. Called by scriber when scribing a new plot.
func (b *Plot) AddInteraction(id InteractionID, tx *Interaction) error {
	// hash the new interaction hash with the running state
	b.hasher.Write(id[:])

	// update the hash list root to account for plotroot amount change
	var err error
	b.Header.HashListRoot, err = addPlotrootToHashListRoot(b.hasher, b.Interactions[0])
	if err != nil {
		return err
	}

	// append the new interaction to the list
	b.Interactions = append(b.Interactions, tx)
	b.Header.InteractionCount += 1
	return nil
}

// Compute a hash list root of all interaction hashes
func computeHashListRoot(hasher hash.Hash, interactions []*Interaction) (InteractionID, error) {
	if hasher == nil {
		hasher = sha3.New256()
	}

	// don't include plotroot in the first round
	for _, tx := range interactions[1:] {
		id, err := tx.ID()
		if err != nil {
			return InteractionID{}, err
		}
		hasher.Write(id[:])
	}

	// add the plotroot last
	return addPlotrootToHashListRoot(hasher, interactions[0])
}

// Add the plotroot to the hash list root
func addPlotrootToHashListRoot(hasher hash.Hash, plotroot *Interaction) (InteractionID, error) {
	// get the root of all of the non-plotroot interaction hashes
	rootHashWithoutPlotroot := hasher.Sum(nil)

	// add the plotroot separately
	// this made adding new interactions while scribing more efficient in a financial context
	id, err := plotroot.ID()
	if err != nil {
		return InteractionID{}, err
	}

	// hash the plotroot hash with the interaction list root hash
	rootHash := sha3.New256()
	rootHash.Write(id[:])
	rootHash.Write(rootHashWithoutPlotroot[:])

	// we end up with a sort of modified hash list root of the form:
	// HashListRoot = H(TXID[0] | H(TXID[1] | ... | TXID[N-1]))
	var hashListRoot InteractionID
	copy(hashListRoot[:], rootHash.Sum(nil))
	return hashListRoot, nil
}

// Compute plot work given its target
func computePlotWork(target PlotID) *big.Int {
	plotWorkInt := big.NewInt(0)
	targetInt := target.GetBigInt()
	if targetInt.Cmp(plotWorkInt) <= 0 {
		return plotWorkInt
	}
	// plot work = 2**256 / (target+1)
	maxInt := new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil)
	targetInt.Add(targetInt, big.NewInt(1))
	return plotWorkInt.Div(maxInt, targetInt)
}

// Compute cumulative thread work given a plot's target and the previous thread work
func computeThreadWork(target, threadWork PlotID) (newThreadWork PlotID) {
	plotWorkInt := computePlotWork(target)
	threadWorkInt := threadWork.GetBigInt()
	threadWorkInt = threadWorkInt.Add(threadWorkInt, plotWorkInt)
	newThreadWork.SetBigInt(threadWorkInt)
	return
}

// ID computes an ID for a given plot header.
func (header PlotHeader) ID() (PlotID, error) {
	headerJson, err := json.Marshal(header)
	if err != nil {
		return PlotID{}, err
	}
	return sha3.Sum256([]byte(headerJson)), nil
}

// IDFast computes an ID for a given plot header when scribing.
func (header *PlotHeader) IDFast(scriberNum int) (*big.Int, int64) {
	if header.hasher == nil {
		header.hasher = NewPlotHeaderHasher()
	}
	return header.hasher.Update(scriberNum, header)
}

// Compare returns true if the header indicates it is a better thread than "theirHeader" up to both points.
// "thisWhen" is the timestamp of when we stored this plot header.
// "theirWhen" is the timestamp of when we stored "theirHeader".
func (header PlotHeader) Compare(theirHeader *PlotHeader, thisWhen, theirWhen int64) bool {
	thisWorkInt := header.ThreadWork.GetBigInt()
	theirWorkInt := theirHeader.ThreadWork.GetBigInt()

	// most work wins
	if thisWorkInt.Cmp(theirWorkInt) > 0 {
		return true
	}
	if thisWorkInt.Cmp(theirWorkInt) < 0 {
		return false
	}

	// tie goes to the plot we stored first
	if thisWhen < theirWhen {
		return true
	}
	if thisWhen > theirWhen {
		return false
	}

	// if we still need to break a tie go by the lesser id
	thisID, err := header.ID()
	if err != nil {
		panic(err)
	}
	theirID, err := theirHeader.ID()
	if err != nil {
		panic(err)
	}
	return thisID.GetBigInt().Cmp(theirID.GetBigInt()) < 0
}

// String implements the Stringer interface
func (id PlotID) String() string {
	return hex.EncodeToString(id[:])
}

// MarshalJSON marshals PlotID as a hex string.
func (id PlotID) MarshalJSON() ([]byte, error) {
	s := "\"" + id.String() + "\""
	return []byte(s), nil
}

// UnmarshalJSON unmarshals PlotID hex string to PlotID.
func (id *PlotID) UnmarshalJSON(b []byte) error {
	if len(b) != 64+2 {
		return fmt.Errorf("Invalid plot ID")
	}
	idBytes, err := hex.DecodeString(string(b[1 : len(b)-1]))
	if err != nil {
		return err
	}
	copy(id[:], idBytes)
	return nil
}

// SetBigInt converts from big.Int to PlotID.
func (id *PlotID) SetBigInt(i *big.Int) *PlotID {
	intBytes := i.Bytes()
	if len(intBytes) > 32 {
		panic("Too much work")
	}
	for i := 0; i < len(id); i++ {
		id[i] = 0x00
	}
	copy(id[32-len(intBytes):], intBytes)
	return id
}

// GetBigInt converts from PlotID to big.Int.
func (id PlotID) GetBigInt() *big.Int {
	return new(big.Int).SetBytes(id[:])
}
