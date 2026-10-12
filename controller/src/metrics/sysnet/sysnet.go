package sysnet

// Counts represents active TCP and UDP socket counts.
type Counts struct {
	TCP uint64
	UDP uint64
}

// ReadSocketCounts returns current TCP and UDP socket counts.
func ReadSocketCounts() (Counts, error) {
	return readSocketCounts()
}
