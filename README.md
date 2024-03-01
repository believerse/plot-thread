# Plot Thread
Adapted from [Cruzbit](https://github.com/cruzbit/cruzbit), a clean and simple decentralised p2p ledger implementation.

## Abstract

Plot-Thread: 1st person identification of continuity as a coordinated thread of deliberate plots consisting of complete interactions.

Block-Chain: 3rd person representation of duration as a competitive chain of arbitrary blocks consisting of fractional transactions.

## Getting started

1. Fork this repository (important for setting up and running a unique instance)
2. Install [Go](https://golang.org/doc/install)
3. Scribe a unique Genesis Plot, as there is no global instance (see `genesis.go` for more details)
4. Install the [keyholder](https://github.com/believerse/plot-thread/tree/main/keyholder)
5. Run the keyholder and issue a `newkey` command. Record the public key
6. Install the [client](https://github.com/believerse/plot-thread/tree/main/client)
7. Run the client using the public key from step 5. as the `-pubkey` argument

Complete steps for installation of Go and the ledger binaries on Linux can be found [here](https://gist.github.com/setanimals/f562ed7dd1c69af3fbe960c7b9502615).
