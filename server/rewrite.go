package server

import (
	"net/url"
	"strings"

	"github.com/madeddie/opds-aggregator/opds"
)

// rewriteFeedLinks rewrites all links in a feed to go through the aggregator proxy.
// Navigation links become /opds/source/{slug}/... paths.
// Acquisition/image links become /opds/download/{slug}?url=... for proxying.
func rewriteFeedLinks(feed *opds.Feed, slug, baseUpstreamURL, sourceRootURL, proxyPrefix string) *opds.Feed {
	// Deep copy to avoid mutating the cache.
	out := *feed
	out.Links = rewriteLinks(feed.Links, slug, baseUpstreamURL, sourceRootURL, proxyPrefix)
	out.Entries = make([]opds.Entry, len(feed.Entries))
	for i, e := range feed.Entries {
		out.Entries[i] = e
		out.Entries[i].Links = rewriteLinks(e.Links, slug, baseUpstreamURL, sourceRootURL, proxyPrefix)
	}
	return &out
}

func rewriteLinks(links []opds.Link, slug, baseUpstreamURL, sourceRootURL, proxyPrefix string) []opds.Link {
	if len(links) == 0 {
		return nil
	}
	out := make([]opds.Link, len(links))
	for i, l := range links {
		out[i] = l
		out[i].Href = rewriteHref(l, slug, baseUpstreamURL, sourceRootURL, proxyPrefix)
	}
	return out
}

func rewriteHref(l opds.Link, slug, baseUpstreamURL, sourceRootURL, proxyPrefix string) string {
	href := resolveURL(baseUpstreamURL, l.Href)

	// Acquisition and image links get proxied through the download endpoint.
	if isAcquisitionRel(l.Rel) || opds.IsImageRel(l.Rel) {
		return proxyPrefix + "/opds/download/" + slug + "?url=" + url.QueryEscape(href)
	}

	// Search links — check before navigation type to avoid misclassifying
	// search templates that have an OPDS feed type (e.g. application/atom+xml).
	if l.Rel == opds.RelSearch {
		return proxyPrefix + "/opds/search/" + slug + "?upstream=" + url.QueryEscape(href)
	}

	// Navigation-type links get rewritten to /opds/source/{slug}/...
	// Use sourceRootURL for makeRelativePath so that paths are relative to the
	// source root, matching what resolveFeed/joinURL expects when reconstructing
	// upstream URLs.
	if isOPDSFeedType(l.Type) || opds.IsNavigationRel(l.Rel) {
		relPath := makeRelativePath(sourceRootURL, href)
		return proxyPrefix + "/opds/source/" + slug + "/" + relPath
	}

	// Everything else (external links, etc.) gets proxied as a download.
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return proxyPrefix + "/opds/download/" + slug + "?url=" + url.QueryEscape(href)
	}

	return href
}

func isAcquisitionRel(rel string) bool {
	switch rel {
	case opds.RelAcquisition, opds.RelOpenAccess, opds.RelBorrow,
		opds.RelBuy, opds.RelSample, opds.RelSubscribe:
		return true
	}
	return false
}

func isOPDSFeedType(mediaType string) bool {
	return strings.Contains(mediaType, "opds-catalog") ||
		strings.Contains(mediaType, "atom+xml")
}

func makeRelativePath(base, full string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return full
	}
	fullURL, err := url.Parse(full)
	if err != nil {
		return full
	}

	// Different host — can't make relative, encode the full URL.
	if baseURL.Host != fullURL.Host {
		return "ext?url=" + url.QueryEscape(full)
	}

	// Normalize the base path to always end with / for prefix stripping.
	basePath := strings.TrimSuffix(baseURL.Path, "/") + "/"
	rel := strings.TrimPrefix(fullURL.Path, basePath)
	// Also handle the case where fullURL.Path equals baseURL.Path exactly.
	if rel == fullURL.Path {
		rel = strings.TrimPrefix(fullURL.Path, strings.TrimSuffix(baseURL.Path, "/"))
		rel = strings.TrimPrefix(rel, "/")
	}
	if fullURL.RawQuery != "" {
		rel += "?" + fullURL.RawQuery
	}
	return rel
}

func resolveURL(base, ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return ref
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	// Ensure the base path is treated as a directory so relative refs append
	// instead of replacing the last segment (e.g., /opds + "foo" → /opds/foo).
	if !strings.HasSuffix(baseURL.Path, "/") {
		baseURL.Path += "/"
	}
	return baseURL.ResolveReference(refURL).String()
}

// joinURL reconstructs an upstream URL by joining a base URL with a relative
// sub-path and optional query string. Unlike resolveURL, this uses simple
// string concatenation which is correct for round-tripping paths created by
// makeRelativePath.
func joinURL(base, subPath, rawQuery string) string {
	u := strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(subPath, "/")
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}
