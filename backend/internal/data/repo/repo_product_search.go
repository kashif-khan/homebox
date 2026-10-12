package repo

type BarcodeProduct struct {
	SearchEngineName string `json:"search_engine_name"`

	// Identifications
	ModelNumber  string `json:"modelNumber"`
	Manufacturer string `json:"manufacturer"`

	// Extras
	Country string `json:"notes"`
	Barcode string `json:"barcode"`

	ImageURL    string `json:"imageURL"`
	ImageBase64 string `json:"imageBase64"`

	// Set by the web enrichment lookup so the user can judge how far to trust a match.
	Confidence float64 `json:"confidence,omitempty"`
	SourceURL  string  `json:"sourceURL,omitempty"`

	Item EntityCreate `json:"item"`
}
