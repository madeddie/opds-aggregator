package opds

import (
	"encoding/xml"
	"fmt"
	"io"
)

// Render writes the feed as OPDS/Atom XML to w, including the XML declaration.
func Render(w io.Writer, feed *Feed) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return fmt.Errorf("opds: write header: %w", err)
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(feed); err != nil {
		return fmt.Errorf("opds: encode feed: %w", err)
	}
	return enc.Flush()
}

// RenderBytes returns the feed as OPDS/Atom XML bytes.
func RenderBytes(feed *Feed) ([]byte, error) {
	data, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("opds: marshal feed: %w", err)
	}
	return append([]byte(xml.Header), data...), nil
}
