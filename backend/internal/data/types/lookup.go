package types

// LookupCandidate is one possible match for an item, as found by the product
// lookup service. It is stored as JSON on the item's lookup and shown to the
// user for review; nothing in it is applied to the item automatically.
type LookupCandidate struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Manufacturer string   `json:"manufacturer"`
	ModelNumber  string   `json:"modelNumber"`
	Country      string   `json:"country"`
	Barcode      string   `json:"barcode"`
	ImageURL     string   `json:"imageURL"` // the first of ImageURLs, kept for older readers
	ImageURLs    []string `json:"imageURLs"`
	SourceURL    string   `json:"sourceURL"`
	Confidence   float64  `json:"confidence"`
	Sources      []string `json:"sources"`
}
