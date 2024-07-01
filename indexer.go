package plotthread

import (
	"encoding/base64"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ed25519"
)

type Indexer struct {
	plotStore       PlotStorage
	ledger           Ledger
	processor        *Processor
	latestPlotID 	 PlotID
	latestHeight     int64
	txGraph          *Graph
	shutdownChan     chan struct{}
	wg               sync.WaitGroup
}

func NewIndexer(
	plotStore PlotStorage,
	ledger Ledger,
	processor *Processor,
	genesisPlotID PlotID,
) *Indexer {
	return &Indexer{
		plotStore:       plotStore,
		ledger:           ledger,
		processor:        processor,
		latestPlotID:    genesisPlotID,
		latestHeight:     0,
		txGraph:          NewGraph(),
		shutdownChan:     make(chan struct{}),
	}
}

// Run executes the indexer's main loop in its own goroutine.
func (idx *Indexer) Run() {
	idx.wg.Add(1)
	go idx.run()
}

func (idx *Indexer) run() {
	defer idx.wg.Done()

	ticker := time.NewTicker(30 * time.Second)

	// don't start indexing until we think we're synced.
	// we're just wasting time and slowing down the sync otherwise
	ibd, _, err := IsInitialPlotDownload(idx.ledger, idx.plotStore)
	if err != nil {
		panic(err)
	}
	if ibd {
		log.Printf("Indexer waiting for plotthread sync\n")
	ready:
		for {
			select {
			case _, ok := <-idx.shutdownChan:
				if !ok {
					log.Printf("Indexer shutting down...\n")
					return
				}
			case <-ticker.C:
				var err error
				ibd, _, err = IsInitialPlotDownload(idx.ledger, idx.plotStore)
				if err != nil {
					panic(err)
				}
				if !ibd {
					// time to start indexing
					break ready
				}
			}
		}
	}

	ticker.Stop()

	header, _, err := idx.plotStore.GetPlotHeader(idx.latestPlotID)
	if err != nil {
		log.Println(err)
		return
	}
	if header == nil {
		// don't have it
		log.Println(err)
		return
	}
	branchType, err := idx.ledger.GetBranchType(idx.latestPlotID)
	if err != nil {
		log.Println(err)
		return
	}
	if branchType != MAIN {
		// not on the main branch
		log.Println(err)
		return
	}

	var height int64 = header.Height
	for {
		nextID, err := idx.ledger.GetPlotIDForHeight(height)
		if err != nil {
			log.Println(err)
			return
		}
		if nextID == nil {
			height -= 1
			break
		}

		plot, err := idx.plotStore.GetPlot(*nextID)
		if err != nil {
			// not found
			log.Println(err)
			return
		}

		if plot == nil {
			// not found
			log.Printf("No plot found with ID %v", nextID)
			return
		}

		idx.indexRepresentations(plot, *nextID, true)

		height += 1
	}	
	
	log.Printf("Finished indexing at height %v", idx.latestHeight)
	log.Printf("Latest indexed plotID: %v", idx.latestPlotID)
	
	idx.rankGraph()
	

	// register for tip changes
	tipChangeChan := make(chan TipChange, 1)
	idx.processor.RegisterForTipChange(tipChangeChan)
	defer idx.processor.UnregisterForTipChange(tipChangeChan)

	for {
		select {
		case tip := <-tipChangeChan:			
			log.Printf("Indexer received notice of new tip plot: %s at height: %d\n", tip.PlotID, tip.Plot.Header.Height)
			idx.indexRepresentations(tip.Plot, tip.PlotID, tip.Connect)
			if !tip.More {
				idx.rankGraph()
			}
		case _, ok := <-idx.shutdownChan:
			if !ok {
				log.Printf("Indexer shutting down...\n")
				return
			}
		}
	}
}

func pubKeyToString(ppk ed25519.PublicKey) string{
	return base64.StdEncoding.EncodeToString(ppk[:])
}

func (idx *Indexer) rankGraph(){
	log.Printf("Indexer commencing ranking at height: %d\n", idx.latestHeight)
	idx.txGraph.Rank(1.0, 1e-6)
	log.Printf("Ranking finished")
}

func (idx *Indexer) indexRepresentations(plot *Plot, id PlotID, increment bool) {
	idx.latestPlotID = id
	idx.latestHeight = plot.Header.Height

	for i := 0; i < len(plot.Representations); i++ {
		tx := plot.Representations[i]

		if increment {
			idx.txGraph.Link(pubKeyToString(tx.From), pubKeyToString(tx.To), 1)
		} else {
			idx.txGraph.Link(pubKeyToString(tx.From), pubKeyToString(tx.To), -1)
		}
	}
}

// Shutdown stops the indexer synchronously.
func (idx *Indexer) Shutdown() {
	close(idx.shutdownChan)
	idx.wg.Wait()
	log.Printf("Indexer shutdown\n")
}

type node struct {
	label    string
	ranking     float64
	outbound float64
}

// Graph holds node and edge data.
type Graph struct {
	index map[string]uint32
	nodes map[uint32]*node
	edges map[uint32](map[uint32]float64)
}

// NewGraph initializes and returns a new graph.
func NewGraph() *Graph {
	return &Graph{
		edges: make(map[uint32](map[uint32]float64)),
		nodes: make(map[uint32]*node),
		index: make(map[string]uint32),
	}
}

// Link creates a weighted edge between a source-target node pair.
// If the edge already exists, the weight is incremented.
func (graph *Graph) Link(source, target string, weight float64) {
	if _, ok := graph.index[source]; !ok {
		index := uint32(len(graph.index))
		graph.index[source] = index
		graph.nodes[index] = &node{
			ranking:     0,
			outbound: 0,
			label:    source,
		}
	}

	if _, ok := graph.index[target]; !ok {
		index := uint32(len(graph.index))
		graph.index[target] = index
		graph.nodes[index] = &node{
			ranking:     0,
			outbound: 0,
			label:    target,
		}
	}

	sIndex := graph.index[source]
	tIndex := graph.index[target]

	if _, ok := graph.edges[sIndex]; !ok {
		graph.edges[sIndex] = map[uint32]float64{}
	}

	graph.nodes[sIndex].outbound += weight
	graph.edges[sIndex][tIndex] += weight
}

func (g *Graph) ToDOT(pubKey string) string {
	includedNodes := []uint32 {}

	pkInt, ok := g.index[pubKey]	
	if !ok {
		dpkInt, dpkOk := g.index["AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="]
		if dpkOk {
			pkInt = dpkInt
			ok = dpkOk
		}		
	}

	if ok {
		includedNodes = append(includedNodes, pkInt)
	}


	var builder strings.Builder
	builder.WriteString("digraph G {\n")

	for from, edge := range g.edges {
		for to, weight := range edge {
			if ok && (from == pkInt || to == pkInt){				
				builder.WriteString(fmt.Sprintf("  \"%d\" -> \"%d\" [weight=\"%.f\"];\n", from, to, weight))
				if from == pkInt{
					includedNodes = append(includedNodes, to)
				}else{
					includedNodes = append(includedNodes, from)
				}
			}			
		}
	}

	// Add nodes with ranks
	for _, id := range includedNodes {		
		builder.WriteString(fmt.Sprintf("  \"%d\" [label=\"%s\", ranking=\"%f\"];\n", id, g.nodes[id].label, g.nodes[id].ranking))		
	}

	builder.WriteString("}\n")
	return builder.String()
}

func (g *Graph) rankings(pubKeys []ed25519.PublicKey) map[string]float64 {

	rnks := make(map[string]float64)

	if len(pubKeys) > 0 {
		for _, key := range pubKeys {
			keyStr := pubKeyToString(key)
			index := g.index[keyStr]
			rnks[keyStr] = g.nodes[index].ranking
		}
	}else {
		//return all keys
		//TODO: limit by top ranking
		for key, id := range g.index {
			rnks[key] = g.nodes[id].ranking
		}
	}

	return rnks
}


// https://github.com/alixaxel/pagerank/blob/master/pagerank.go
// Rank computes the RepresentivityRank of every node in the directed graph.
// α (alpha) is the damping factor, usually set to 0.85.
// ε (epsilon) is the convergence criteria, usually set to a tiny value.
//
// This method will run as many iterations as needed, until the graph converges.
func (graph *Graph) Rank(alpha, epsilon float64) {

	normalizedWeights := make(map[uint32](map[uint32]float64))

	Δ := float64(1.0)
	inverse := 1 / float64(len(graph.nodes))

	// Normalize all the edge weights so that their sum amounts to 1.
	for source := range graph.edges {
		if graph.nodes[source].outbound > 0 {
			normalizedWeights[source] = make(map[uint32]float64)
			for target := range graph.edges[source] {
				normalizedWeights[source][target] = graph.edges[source][target] / graph.nodes[source].outbound
			}
		}
	}

	for key := range graph.nodes {
		graph.nodes[key].ranking = inverse
	}

	for Δ > epsilon {
		leak := float64(0)
		nodes := map[uint32]float64{}

		for key, value := range graph.nodes {
			nodes[key] = value.ranking

			if value.outbound == 0 {
				leak += value.ranking
			}

			graph.nodes[key].ranking = 0
		}

		leak *= alpha

		for source := range graph.nodes {
			for target, weight := range normalizedWeights[source] {
				graph.nodes[target].ranking += alpha * nodes[source] * weight
			}

			graph.nodes[source].ranking += (1-alpha)*inverse + leak*inverse
		}

		Δ = 0

		for key, value := range graph.nodes {
			Δ += math.Abs(value.ranking - nodes[key])
		}
	}
}

// Reset clears all the current graph data.
func (graph *Graph) Reset() {
	graph.edges = make(map[uint32](map[uint32]float64))
	graph.nodes = make(map[uint32]*node)
	graph.index = make(map[string]uint32)
}

