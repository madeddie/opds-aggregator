package opds

import (
	"encoding/xml"
	"fmt"
	"io"
)

// Parse reads an OPDS/Atom XML feed from r and returns the parsed Feed.
func Parse(r io.Reader) (*Feed, error) {
	var feed Feed
	dec := xml.NewDecoder(r)
	dec.Strict = false
	if err := dec.Decode(&feed); err != nil {
		return nil, fmt.Errorf("opds: parse feed: %w", err)
	}
	return &feed, nil
}

// ParseBytes parses an OPDS/Atom XML feed from raw bytes.
func ParseBytes(data []byte) (*Feed, error) {
	var feed Feed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("opds: parse feed: %w", err)
	}
	return &feed, nil
}
