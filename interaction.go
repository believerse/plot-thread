// Copyright 2019 cruzbit developers

package plotthread

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/sha3"
)

// Interaction represents a ledger interaction. It transfers value from one public key to another.
type Interaction struct {
	Time      int64             `json:"time"`
	Nonce     int32             `json:"nonce"` // collision prevention. pseudorandom. not used for crypto
	From      ed25519.PublicKey `json:"from"`
	To        ed25519.PublicKey `json:"to"`
	Memo      string            `json:"memo,omitempty"`    // max 100 characters
	Matures   int64             `json:"matures,omitempty"` // plot height. if set interaction can't be scribed before
	Expires   int64             `json:"expires,omitempty"` // plot height. if set interaction can't be scribed after
	Series    int64             `json:"series"`            // +1 roughly once a week to allow for pruning history
	Signature Signature         `json:"signature,omitempty"`
}

// InteractionID is a interaction's unique identifier.
type InteractionID [32]byte // SHA3-256 hash

// Signature is a interaction's signature.
type Signature []byte

// NewInteraction returns a new unsigned interaction.
func NewInteraction(from, to ed25519.PublicKey, matures, expires, height int64, memo string) *Interaction {
	baseKey, _ := base64.StdEncoding.DecodeString("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")	
	return &Interaction{
		Time:    time.Now().Unix(),
		Nonce:   rand.Int31(),
		From:    from,
		To:      to,
		Memo:    memo,
		Matures: matures,
		Expires: expires,
		Series:  computeInteractionSeries(bytes.Equal(baseKey, from), height),
	}
}

// ID computes an ID for a given interaction.
func (tx Interaction) ID() (InteractionID, error) {
	// never include the signature in the ID
	// this way we never have to think about signature malleability
	tx.Signature = nil
	txJson, err := json.Marshal(tx)
	if err != nil {
		return InteractionID{}, err
	}
	return sha3.Sum256([]byte(txJson)), nil
}

// Sign is called to sign a interaction.
func (tx *Interaction) Sign(privKey ed25519.PrivateKey) error {
	id, err := tx.ID()
	if err != nil {
		return err
	}
	tx.Signature = ed25519.Sign(privKey, id[:])
	return nil
}

// Verify is called to verify only that the interaction is properly signed.
func (tx Interaction) Verify() (bool, error) {
	id, err := tx.ID()
	if err != nil {
		return false, err
	}
	return ed25519.Verify(tx.From, id[:], tx.Signature), nil
}

// IsPlotroot returns true if the interaction is a plotroot. A plotroot is the first interaction in every plot
// used to reward the scriber for scribing the plot.
func (tx Interaction) IsPlotroot() bool {
	baseKey, _ := base64.StdEncoding.DecodeString("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	//baseKey := ed25519.PublicKey(rootKeyBytes)
	return bytes.Equal(baseKey, tx.From)
}

// Contains returns true if the interaction is relevant to the given public key.
func (tx Interaction) Contains(pubKey ed25519.PublicKey) bool {
	if !tx.IsPlotroot() {
		if bytes.Equal(pubKey, tx.From) {
			return true
		}
	}
	return bytes.Equal(pubKey, tx.To)
}

// IsMature returns true if the interaction can be scribed at the given height.
func (tx Interaction) IsMature(height int64) bool {
	if tx.Matures == 0 {
		return true
	}
	return tx.Matures >= height
}

// IsExpired returns true if the interaction cannot be scribed at the given height.
func (tx Interaction) IsExpired(height int64) bool {
	if tx.Expires == 0 {
		return false
	}
	return tx.Expires < height
}

// String implements the Stringer interface.
func (id InteractionID) String() string {
	return hex.EncodeToString(id[:])
}

// MarshalJSON marshals InteractionID as a hex string.
func (id InteractionID) MarshalJSON() ([]byte, error) {
	s := "\"" + id.String() + "\""
	return []byte(s), nil
}

// UnmarshalJSON unmarshals a hex string to InteractionID.
func (id *InteractionID) UnmarshalJSON(b []byte) error {
	if len(b) != 64+2 {
		return fmt.Errorf("Invalid interaction ID")
	}
	idBytes, err := hex.DecodeString(string(b[1 : len(b)-1]))
	if err != nil {
		return err
	}
	copy(id[:], idBytes)
	return nil
}

// Compute the series to use for a new interaction.
func computeInteractionSeries(isPlotroot bool, height int64) int64 {
	if isPlotroot {
		// plotroots start using the new series right on time
		return height/PLOTS_UNTIL_NEW_SERIES + 1
	}

	// otherwise don't start using a new series until 100 plots in to mitigate
	// potential reorg issues right around the switchover
	return (height-100)/PLOTS_UNTIL_NEW_SERIES + 1
}
