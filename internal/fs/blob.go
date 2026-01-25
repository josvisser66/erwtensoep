package fs

type Blob struct {
	ID string `json:"id"`
	// Size is stored as a string to avoid JSON number precision issues for large values.
	Size   string   `json:"size"`
	Blocks []string `json:"blocks"`

	// size caches the parsed integer form of Size for repeated arithmetic.
	size int64
}
