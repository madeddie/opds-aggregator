// Package opds provides types, parsing, and rendering for OPDS 1.2 Atom feeds.
package opds

import "encoding/xml"

// XML namespaces used in OPDS catalogs.
const (
	NSAtom       = "http://www.w3.org/2005/Atom"
	NSDC         = "http://purl.org/dc/terms/"
	NSOPDS       = "http://opds-spec.org/2010/catalog"
	NSOpenSearch = "http://a9.com/-/spec/opensearch/1.1/"
	NSThr        = "http://purl.org/syndication/thread/1.0"
	NSFH         = "http://purl.org/syndication/history/1.0"

	// Link relations.
	RelSelf         = "self"
	RelStart        = "start"
	RelSubsection   = "subsection"
	RelFirst        = "first"
	RelPrevious     = "previous"
	RelNext         = "next"
	RelLast         = "last"
	RelSearch       = "search"
	RelFacet        = "http://opds-spec.org/facet"
	RelAcquisition  = "http://opds-spec.org/acquisition"
	RelOpenAccess   = "http://opds-spec.org/acquisition/open-access"
	RelBorrow       = "http://opds-spec.org/acquisition/borrow"
	RelBuy          = "http://opds-spec.org/acquisition/buy"
	RelSample       = "http://opds-spec.org/acquisition/sample"
	RelSubscribe    = "http://opds-spec.org/acquisition/subscribe"
	RelImage        = "http://opds-spec.org/image"
	RelThumbnail    = "http://opds-spec.org/image/thumbnail"
	RelSortNew      = "http://opds-spec.org/sort/new"
	RelSortPopular  = "http://opds-spec.org/sort/popular"
	RelFeatured     = "http://opds-spec.org/featured"
	RelRecommended  = "http://opds-spec.org/recommended"
	RelShelf        = "http://opds-spec.org/shelf"
	RelSubscriptions = "http://opds-spec.org/subscriptions"
	RelAlternate    = "alternate"
	RelRelated      = "related"

	// Media types.
	MediaTypeOPDSNav  = "application/atom+xml;profile=opds-catalog;kind=navigation"
	MediaTypeOPDSAcq  = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	MediaTypeOPDSEntry = "application/atom+xml;type=entry;profile=opds-catalog"
	MediaTypeAtom     = "application/atom+xml"
	MediaTypeOpenSearch = "application/opensearchdescription+xml"
)

// Feed represents an Atom feed with OPDS extensions.
type Feed struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Updated string   `xml:"updated"`
	Icon    string   `xml:"icon,omitempty"`
	Author  *Author  `xml:"author,omitempty"`
	Links   []Link   `xml:"link"`
	Entries []Entry  `xml:"entry"`

	// Pagination (RFC 5005).
	TotalResults int `xml:"http://a9.com/-/spec/opensearch/1.1/ totalResults,omitempty"`
	ItemsPerPage int `xml:"http://a9.com/-/spec/opensearch/1.1/ itemsPerPage,omitempty"`
	StartIndex   int `xml:"http://a9.com/-/spec/opensearch/1.1/ startIndex,omitempty"`

	// Feed History (fh:complete).
	Complete *struct{} `xml:"http://purl.org/syndication/history/1.0 complete,omitempty"`
}

// Entry represents an Atom entry with OPDS extensions.
type Entry struct {
	XMLName   xml.Name   `xml:"entry"`
	ID        string     `xml:"id"`
	Title     string     `xml:"title"`
	Updated   string     `xml:"updated"`
	Published string     `xml:"published,omitempty"`
	Summary   *Text      `xml:"summary,omitempty"`
	Content   *Text      `xml:"content,omitempty"`
	Rights    string     `xml:"rights,omitempty"`
	Language  string     `xml:"http://purl.org/dc/terms/ language,omitempty"`
	Issued    string     `xml:"http://purl.org/dc/terms/ issued,omitempty"`
	Publisher string     `xml:"http://purl.org/dc/terms/ publisher,omitempty"`
	Authors   []Author   `xml:"author,omitempty"`
	Categories []Category `xml:"category,omitempty"`
	Links     []Link     `xml:"link"`
	Prices    []Price    `xml:"http://opds-spec.org/2010/catalog price,omitempty"`
}

// Author represents an Atom author or contributor.
type Author struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

// Text represents text content that may have a type attribute (text, html, xhtml).
type Text struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

// Link represents an Atom link with OPDS extensions.
type Link struct {
	Rel          string `xml:"rel,attr,omitempty"`
	Href         string `xml:"href,attr"`
	Type         string `xml:"type,attr,omitempty"`
	Title        string `xml:"title,attr,omitempty"`
	Count        int    `xml:"http://purl.org/syndication/thread/1.0 count,attr,omitempty"`
	FacetGroup   string `xml:"http://opds-spec.org/2010/catalog facetGroup,attr,omitempty"`
	ActiveFacet  string `xml:"http://opds-spec.org/2010/catalog activeFacet,attr,omitempty"`
	Length       int64  `xml:"length,attr,omitempty"`

	IndirectAcq []IndirectAcquisition `xml:"http://opds-spec.org/2010/catalog indirectAcquisition,omitempty"`
}

// IndirectAcquisition describes the media type chain for indirect acquisitions.
type IndirectAcquisition struct {
	Type    string                `xml:"type,attr"`
	Children []IndirectAcquisition `xml:"http://opds-spec.org/2010/catalog indirectAcquisition,omitempty"`
}

// Price represents the price of an acquisition.
type Price struct {
	CurrencyCode string  `xml:"currencycode,attr"`
	Value        string  `xml:",chardata"`
}

// Category represents an Atom category.
type Category struct {
	Term   string `xml:"term,attr"`
	Label  string `xml:"label,attr,omitempty"`
	Scheme string `xml:"scheme,attr,omitempty"`
}

// IsNavigationFeed returns true if the feed looks like a navigation feed
// (all entries have subsection links and no acquisition links).
func (f *Feed) IsNavigationFeed() bool {
	if len(f.Entries) == 0 {
		return false
	}
	for _, e := range f.Entries {
		if e.HasAcquisitionLinks() {
			return false
		}
	}
	return true
}

// HasAcquisitionLinks returns true if the entry contains at least one acquisition link.
func (e *Entry) HasAcquisitionLinks() bool {
	for _, l := range e.Links {
		if isAcquisitionRel(l.Rel) {
			return true
		}
	}
	return false
}

// isAcquisitionRel returns true if rel is an OPDS acquisition relation.
func isAcquisitionRel(rel string) bool {
	switch rel {
	case RelAcquisition, RelOpenAccess, RelBorrow, RelBuy, RelSample, RelSubscribe:
		return true
	}
	return false
}

// IsImageRel returns true if rel is an OPDS image/thumbnail relation.
func IsImageRel(rel string) bool {
	return rel == RelImage || rel == RelThumbnail
}

// IsNavigationRel returns true if the link points to another feed (navigation or acquisition).
func IsNavigationRel(rel string) bool {
	switch rel {
	case RelSubsection, RelStart, RelSelf, RelNext, RelAlternate, RelRelated,
		RelSortNew, RelSortPopular, RelFeatured, RelRecommended, RelShelf, RelSubscriptions:
		return true
	}
	return rel == RelFacet
}

// SelfLink returns the link with rel="self", or nil if not found.
func (f *Feed) SelfLink() *Link {
	for i, l := range f.Links {
		if l.Rel == RelSelf {
			return &f.Links[i]
		}
	}
	return nil
}

// NextLink returns the link with rel="next" (pagination), or nil if not found.
func (f *Feed) NextLink() *Link {
	for i, l := range f.Links {
		if l.Rel == RelNext {
			return &f.Links[i]
		}
	}
	return nil
}

// SearchLink returns the search link, or nil if not found.
func (f *Feed) SearchLink() *Link {
	for i, l := range f.Links {
		if l.Rel == RelSearch {
			return &f.Links[i]
		}
	}
	return nil
}
