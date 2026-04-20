package model

type DataItem struct {
	Source    string         `json:"source"`
	Category  string         `json:"category"`
	Title     string         `json:"title"`
	Value     string         `json:"value"`
	Extra     map[string]any `json:"extra,omitempty"`
	FetchedAt int64          `json:"fetched_at"`
}

type SourceSnapshot struct {
	Status    string     `json:"status"`
	LastFetch int64      `json:"last_fetch"`
	Error     string     `json:"error,omitempty"`
	Items     []DataItem `json:"items"`
}

type SummaryResponse struct {
	UpdatedAt int64                     `json:"updated_at"`
	Sources   map[string]SourceSnapshot `json:"sources"`
}

type CollectorStatus struct {
	Status    string `json:"status"`
	LastFetch int64  `json:"last_fetch"`
	Error     string `json:"error,omitempty"`
}

type HealthResponse struct {
	Status     string                     `json:"status"`
	Collectors map[string]CollectorStatus `json:"collectors"`
}
