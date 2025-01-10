package fs

type Blob struct {
	ID     string   `json:"id"`
	Size   string   `json:"size"` // string because of JSO encoding issues of large numbers
	size   int64    // cache for Size
	Blocks []string `json:"blocks"`
}
