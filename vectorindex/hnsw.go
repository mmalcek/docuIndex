package vectorindex

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"sync"
	"time"
)

// HNSW implements Hierarchical Navigable Small World graph for approximate nearest neighbor search
type HNSW struct {
	mu sync.RWMutex

	// Configuration
	M        int     // Max connections per layer
	Ml       float64 // Level multiplier (typically 1/ln(M))
	EfConst  int     // Construction ef parameter
	EfSearch int     // Search ef parameter

	// Data
	nodes    map[string]*node // ID -> node
	entryID  string           // Entry point node ID
	maxLevel int              // Current max level

	// Dimension of vectors (set on first insert)
	dimension int

	// Incremental update tracking
	isDirty        bool      // Has unsaved changes
	pendingAdds    int       // Count of nodes added since last save
	pendingDeletes int       // Count of nodes deleted since last save
	lastSaveTime   time.Time // When index was last saved
}

// node represents a point in the graph
type node struct {
	ID      string
	Vector  []float32
	Level   int
	Friends [][]string // Friends at each level
}

// SearchResult represents a search result
type SearchResult struct {
	ID       string
	Distance float32
	Score    float32
}

// Config holds HNSW configuration
type Config struct {
	M        int // Max connections per layer (default: 16)
	EfConst  int // Construction ef (default: 200)
	EfSearch int // Search ef (default: 50)
}

// DefaultConfig returns default HNSW configuration
func DefaultConfig() *Config {
	return &Config{
		M:        16,
		EfConst:  200,
		EfSearch: 50,
	}
}

// NewHNSW creates a new HNSW index
func NewHNSW(cfg *Config) *HNSW {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	return &HNSW{
		M:        cfg.M,
		Ml:       1.0 / math.Log(float64(cfg.M)),
		EfConst:  cfg.EfConst,
		EfSearch: cfg.EfSearch,
		nodes:    make(map[string]*node),
		maxLevel: -1,
	}
}

// Add inserts a vector into the index
func (h *HNSW) Add(id string, vector []float32) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.dimension == 0 {
		h.dimension = len(vector)
	} else if len(vector) != h.dimension {
		return fmt.Errorf("dimension mismatch: expected %d, got %d", h.dimension, len(vector))
	}

	// Check if already exists
	if _, exists := h.nodes[id]; exists {
		// Update existing node
		h.nodes[id].Vector = vector
		return nil
	}

	// Calculate random level for new node
	level := h.randomLevel()

	// Create new node
	newNode := &node{
		ID:      id,
		Vector:  vector,
		Level:   level,
		Friends: make([][]string, level+1),
	}

	// Initialize friend lists
	for i := range newNode.Friends {
		newNode.Friends[i] = make([]string, 0, h.M)
	}

	// If first node, set as entry point
	if h.entryID == "" {
		h.nodes[id] = newNode
		h.entryID = id
		h.maxLevel = level
		return nil
	}

	// Find entry point for insertion
	currID := h.entryID

	// Traverse from top level down to level+1
	for lc := h.maxLevel; lc > level; lc-- {
		currID = h.searchLayer(vector, currID, 1, lc)[0].ID
	}

	// Insert at each level from level down to 0
	for lc := min(level, h.maxLevel); lc >= 0; lc-- {
		neighbors := h.searchLayer(vector, currID, h.EfConst, lc)
		selectedNeighbors := h.selectNeighbors(vector, neighbors, h.M)

		// Connect new node to neighbors
		for _, neighbor := range selectedNeighbors {
			newNode.Friends[lc] = append(newNode.Friends[lc], neighbor.ID)
		}

		// Connect neighbors back to new node
		for _, neighbor := range selectedNeighbors {
			neighborNode := h.nodes[neighbor.ID]
			neighborNode.Friends[lc] = append(neighborNode.Friends[lc], id)

			// Prune if too many connections
			if len(neighborNode.Friends[lc]) > h.M {
				neighborNode.Friends[lc] = h.pruneNeighbors(neighborNode, lc)
			}
		}

		if len(neighbors) > 0 {
			currID = neighbors[0].ID
		}
	}

	h.nodes[id] = newNode

	// Update entry point if new node has higher level
	if level > h.maxLevel {
		h.entryID = id
		h.maxLevel = level
	}

	// Track change
	h.isDirty = true
	h.pendingAdds++

	return nil
}

// Delete removes a vector from the index
func (h *HNSW) Delete(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	node, exists := h.nodes[id]
	if !exists {
		return nil // Not found, nothing to do
	}

	// Remove from all neighbors' friend lists
	for level, friends := range node.Friends {
		for _, friendID := range friends {
			friendNode := h.nodes[friendID]
			if friendNode != nil {
				friendNode.Friends[level] = removeFromSlice(friendNode.Friends[level], id)
			}
		}
	}

	delete(h.nodes, id)

	// If deleted node was entry point, find new one
	if h.entryID == id {
		if len(h.nodes) == 0 {
			h.entryID = ""
			h.maxLevel = -1
		} else {
			// Find node with highest level
			maxLevel := -1
			for nid, n := range h.nodes {
				if n.Level > maxLevel {
					maxLevel = n.Level
					h.entryID = nid
				}
			}
			h.maxLevel = maxLevel
		}
	}

	// Track change
	h.isDirty = true
	h.pendingDeletes++

	return nil
}

// Search finds the k nearest neighbors to the query vector
func (h *HNSW) Search(query []float32, k int) ([]SearchResult, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.nodes) == 0 {
		return nil, nil
	}

	if h.dimension > 0 && len(query) != h.dimension {
		return nil, fmt.Errorf("dimension mismatch: expected %d, got %d", h.dimension, len(query))
	}

	// Start from entry point
	currID := h.entryID

	// Traverse from top level down to level 1
	for lc := h.maxLevel; lc > 0; lc-- {
		results := h.searchLayer(query, currID, 1, lc)
		if len(results) > 0 {
			currID = results[0].ID
		}
	}

	// Search at level 0 with ef=efSearch
	ef := h.EfSearch
	if ef < k {
		ef = k
	}

	candidates := h.searchLayer(query, currID, ef, 0)

	// Return top k
	if len(candidates) > k {
		candidates = candidates[:k]
	}

	return candidates, nil
}

// Size returns the number of vectors in the index
func (h *HNSW) Size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.nodes)
}

// IsDirty returns true if there are unsaved changes
func (h *HNSW) IsDirty() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.isDirty
}

// PendingChanges returns the count of pending adds and deletes
func (h *HNSW) PendingChanges() (adds, deletes int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pendingAdds, h.pendingDeletes
}

// LastSaveTime returns when the index was last saved
func (h *HNSW) LastSaveTime() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastSaveTime
}

// MarkClean marks the index as having no pending changes
func (h *HNSW) MarkClean() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.isDirty = false
	h.pendingAdds = 0
	h.pendingDeletes = 0
	h.lastSaveTime = time.Now()
}

// SaveIfDirty saves the index only if there are pending changes
func (h *HNSW) SaveIfDirty(path string) error {
	h.mu.RLock()
	dirty := h.isDirty
	h.mu.RUnlock()

	if !dirty {
		return nil
	}

	return h.SaveToFile(path)
}

// searchLayer searches for nearest neighbors at a specific layer
func (h *HNSW) searchLayer(query []float32, entryID string, ef int, level int) []SearchResult {
	visited := make(map[string]bool)
	candidates := &distanceHeap{}
	results := &distanceHeap{}

	entryNode := h.nodes[entryID]
	if entryNode == nil {
		return nil
	}

	dist := cosineDistance(query, entryNode.Vector)
	visited[entryID] = true

	candidates.Push(SearchResult{ID: entryID, Distance: dist, Score: 1 - dist})
	results.Push(SearchResult{ID: entryID, Distance: dist, Score: 1 - dist})

	for candidates.Len() > 0 {
		// Get nearest unvisited candidate
		curr := candidates.Pop()

		// Check if we should stop
		if results.Len() >= ef && curr.Distance > results.Peek().Distance {
			break
		}

		currNode := h.nodes[curr.ID]
		if currNode == nil || level >= len(currNode.Friends) {
			continue
		}

		// Explore neighbors
		for _, neighborID := range currNode.Friends[level] {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true

			neighborNode := h.nodes[neighborID]
			if neighborNode == nil {
				continue
			}

			dist := cosineDistance(query, neighborNode.Vector)
			result := SearchResult{ID: neighborID, Distance: dist, Score: 1 - dist}

			if results.Len() < ef || dist < results.Peek().Distance {
				candidates.Push(result)
				results.Push(result)

				if results.Len() > ef {
					results.PopMax()
				}
			}
		}
	}

	return results.ToSorted()
}

// selectNeighbors selects the best neighbors using simple heuristic
func (h *HNSW) selectNeighbors(query []float32, candidates []SearchResult, m int) []SearchResult {
	if len(candidates) <= m {
		return candidates
	}

	// Sort by distance
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Distance < candidates[j].Distance
	})

	return candidates[:m]
}

// pruneNeighbors reduces connections to at most M
func (h *HNSW) pruneNeighbors(n *node, level int) []string {
	if len(n.Friends[level]) <= h.M {
		return n.Friends[level]
	}

	// Calculate distances to all friends
	type friendDist struct {
		id   string
		dist float32
	}
	dists := make([]friendDist, len(n.Friends[level]))
	for i, friendID := range n.Friends[level] {
		friendNode := h.nodes[friendID]
		if friendNode != nil {
			dists[i] = friendDist{id: friendID, dist: cosineDistance(n.Vector, friendNode.Vector)}
		} else {
			dists[i] = friendDist{id: friendID, dist: 1.0}
		}
	}

	// Sort by distance
	sort.Slice(dists, func(i, j int) bool {
		return dists[i].dist < dists[j].dist
	})

	// Keep top M
	result := make([]string, h.M)
	for i := 0; i < h.M; i++ {
		result[i] = dists[i].id
	}

	return result
}

// randomLevel generates a random level for a new node
func (h *HNSW) randomLevel() int {
	level := 0
	for rand.Float64() < h.Ml && level < 16 {
		level++
	}
	return level
}

// SaveToFile persists the index to a file
func (h *HNSW) SaveToFile(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if err := h.writeTo(f); err != nil {
		return err
	}

	// Mark clean after successful save
	h.isDirty = false
	h.pendingAdds = 0
	h.pendingDeletes = 0
	h.lastSaveTime = time.Now()

	return nil
}

// LoadFromFile loads the index from a file
func (h *HNSW) LoadFromFile(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist, start fresh
		}
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	return h.readFrom(f)
}

// writeTo writes the index to a writer
func (h *HNSW) writeTo(w io.Writer) error {
	// Write header
	header := make([]byte, 32)
	copy(header[0:4], []byte("HNSW"))
	binary.LittleEndian.PutUint32(header[4:8], 1)                   // Version
	binary.LittleEndian.PutUint32(header[8:12], uint32(len(h.nodes)))
	binary.LittleEndian.PutUint32(header[12:16], uint32(h.dimension))
	binary.LittleEndian.PutUint32(header[16:20], uint32(h.M))
	binary.LittleEndian.PutUint32(header[20:24], uint32(h.maxLevel))
	binary.LittleEndian.PutUint32(header[24:28], uint32(len(h.entryID)))

	if _, err := w.Write(header); err != nil {
		return err
	}

	// Write entry ID
	if _, err := w.Write([]byte(h.entryID)); err != nil {
		return err
	}

	// Write nodes
	for id, node := range h.nodes {
		// Write ID length and ID
		idLen := make([]byte, 4)
		binary.LittleEndian.PutUint32(idLen, uint32(len(id)))
		if _, err := w.Write(idLen); err != nil {
			return err
		}
		if _, err := w.Write([]byte(id)); err != nil {
			return err
		}

		// Write level
		levelBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(levelBuf, uint32(node.Level))
		if _, err := w.Write(levelBuf); err != nil {
			return err
		}

		// Write vector
		for _, f := range node.Vector {
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint32(buf, math.Float32bits(f))
			if _, err := w.Write(buf); err != nil {
				return err
			}
		}

		// Write friends at each level
		for level := 0; level <= node.Level; level++ {
			friends := node.Friends[level]
			countBuf := make([]byte, 4)
			binary.LittleEndian.PutUint32(countBuf, uint32(len(friends)))
			if _, err := w.Write(countBuf); err != nil {
				return err
			}

			for _, friendID := range friends {
				fIDLen := make([]byte, 4)
				binary.LittleEndian.PutUint32(fIDLen, uint32(len(friendID)))
				if _, err := w.Write(fIDLen); err != nil {
					return err
				}
				if _, err := w.Write([]byte(friendID)); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// readFrom reads the index from a reader
func (h *HNSW) readFrom(r io.Reader) error {
	// Read header
	header := make([]byte, 32)
	if _, err := io.ReadFull(r, header); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	if string(header[0:4]) != "HNSW" {
		return fmt.Errorf("invalid file format")
	}

	nodeCount := int(binary.LittleEndian.Uint32(header[8:12]))
	h.dimension = int(binary.LittleEndian.Uint32(header[12:16]))
	h.M = int(binary.LittleEndian.Uint32(header[16:20]))
	h.maxLevel = int(binary.LittleEndian.Uint32(header[20:24]))
	entryIDLen := int(binary.LittleEndian.Uint32(header[24:28]))

	// Read entry ID
	entryIDBuf := make([]byte, entryIDLen)
	if _, err := io.ReadFull(r, entryIDBuf); err != nil {
		return fmt.Errorf("read entry ID: %w", err)
	}
	h.entryID = string(entryIDBuf)

	// Read nodes
	h.nodes = make(map[string]*node, nodeCount)
	for i := 0; i < nodeCount; i++ {
		// Read ID
		idLenBuf := make([]byte, 4)
		if _, err := io.ReadFull(r, idLenBuf); err != nil {
			return fmt.Errorf("read ID length: %w", err)
		}
		idLen := int(binary.LittleEndian.Uint32(idLenBuf))
		idBuf := make([]byte, idLen)
		if _, err := io.ReadFull(r, idBuf); err != nil {
			return fmt.Errorf("read ID: %w", err)
		}
		id := string(idBuf)

		// Read level
		levelBuf := make([]byte, 4)
		if _, err := io.ReadFull(r, levelBuf); err != nil {
			return fmt.Errorf("read level: %w", err)
		}
		level := int(binary.LittleEndian.Uint32(levelBuf))

		// Read vector
		vector := make([]float32, h.dimension)
		for j := 0; j < h.dimension; j++ {
			buf := make([]byte, 4)
			if _, err := io.ReadFull(r, buf); err != nil {
				return fmt.Errorf("read vector: %w", err)
			}
			vector[j] = math.Float32frombits(binary.LittleEndian.Uint32(buf))
		}

		// Read friends
		friends := make([][]string, level+1)
		for lv := 0; lv <= level; lv++ {
			countBuf := make([]byte, 4)
			if _, err := io.ReadFull(r, countBuf); err != nil {
				return fmt.Errorf("read friend count: %w", err)
			}
			count := int(binary.LittleEndian.Uint32(countBuf))
			friends[lv] = make([]string, count)

			for f := 0; f < count; f++ {
				fIDLenBuf := make([]byte, 4)
				if _, err := io.ReadFull(r, fIDLenBuf); err != nil {
					return fmt.Errorf("read friend ID length: %w", err)
				}
				fIDLen := int(binary.LittleEndian.Uint32(fIDLenBuf))
				fIDBuf := make([]byte, fIDLen)
				if _, err := io.ReadFull(r, fIDBuf); err != nil {
					return fmt.Errorf("read friend ID: %w", err)
				}
				friends[lv][f] = string(fIDBuf)
			}
		}

		h.nodes[id] = &node{
			ID:      id,
			Vector:  vector,
			Level:   level,
			Friends: friends,
		}
	}

	h.Ml = 1.0 / math.Log(float64(h.M))

	return nil
}

// cosineDistance calculates cosine distance (1 - similarity)
func cosineDistance(a, b []float32) float32 {
	if len(a) != len(b) {
		return 1.0
	}

	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 1.0
	}

	similarity := dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
	return 1.0 - similarity
}

// removeFromSlice removes an element from a string slice
func removeFromSlice(slice []string, val string) []string {
	for i, v := range slice {
		if v == val {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

// distanceHeap implements a min-heap for search results
type distanceHeap struct {
	items []SearchResult
}

func (h *distanceHeap) Len() int { return len(h.items) }

func (h *distanceHeap) Push(r SearchResult) {
	h.items = append(h.items, r)
	h.up(len(h.items) - 1)
}

func (h *distanceHeap) Pop() SearchResult {
	if len(h.items) == 0 {
		return SearchResult{}
	}
	result := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	if len(h.items) > 0 {
		h.down(0)
	}
	return result
}

func (h *distanceHeap) Peek() SearchResult {
	if len(h.items) == 0 {
		return SearchResult{Distance: math.MaxFloat32}
	}
	return h.items[0]
}

func (h *distanceHeap) PopMax() SearchResult {
	if len(h.items) == 0 {
		return SearchResult{}
	}
	// Find max
	maxIdx := 0
	for i := 1; i < len(h.items); i++ {
		if h.items[i].Distance > h.items[maxIdx].Distance {
			maxIdx = i
		}
	}
	result := h.items[maxIdx]
	h.items = append(h.items[:maxIdx], h.items[maxIdx+1:]...)
	return result
}

func (h *distanceHeap) ToSorted() []SearchResult {
	result := make([]SearchResult, len(h.items))
	copy(result, h.items)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Distance < result[j].Distance
	})
	return result
}

func (h *distanceHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if h.items[i].Distance >= h.items[parent].Distance {
			break
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

func (h *distanceHeap) down(i int) {
	for {
		left := 2*i + 1
		if left >= len(h.items) {
			break
		}
		smallest := left
		right := left + 1
		if right < len(h.items) && h.items[right].Distance < h.items[left].Distance {
			smallest = right
		}
		if h.items[i].Distance <= h.items[smallest].Distance {
			break
		}
		h.items[i], h.items[smallest] = h.items[smallest], h.items[i]
		i = smallest
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
