package hnapi

// Item is the business-facing HN item shape consumed by hn-ssr UI.
type Item struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	By          string  `json:"by,omitempty"`
	Time        int64   `json:"time,omitempty"`
	Title       string  `json:"title,omitempty"`
	URL         string  `json:"url,omitempty"`
	Text        string  `json:"text,omitempty"`
	Score       int     `json:"score,omitempty"`
	Descendants int     `json:"descendants,omitempty"`
	Parent      *int64  `json:"parent,omitempty"`
	Dead        bool    `json:"dead,omitempty"`
	Deleted     bool    `json:"deleted,omitempty"`
	Comments    []*Item `json:"comments"`
}

// firebaseItem is the raw Firebase v0 item JSON.
type firebaseItem struct {
	ID          int64   `json:"id"`
	Deleted     bool    `json:"deleted"`
	Type        string  `json:"type"`
	By          string  `json:"by"`
	Time        int64   `json:"time"`
	Text        string  `json:"text"`
	Dead        bool    `json:"dead"`
	Parent      int64   `json:"parent"`
	Kids        []int64 `json:"kids"`
	URL         string  `json:"url"`
	Score       int     `json:"score"`
	Title       string  `json:"title"`
	Descendants int     `json:"descendants"`
}
