package domain

import (
	"errors"
	"net/url"
	"strings"
)

// trackingParams are query parameters that only identify where a click came
// from. They never change the product or price, so they are dropped: the same
// product shared from an email and from Instagram is then one listing.
var trackingParams = map[string]bool{
	"gclid": true, "fbclid": true, "msclkid": true, "dclid": true, "yclid": true, "igshid": true,
	"mc_cid": true, "mc_eid": true, "_ga": true, "_gl": true, "srsltid": true, "ref": true, "ref_": true,
	"spm": true, "scm": true,
	// Shopify search/recommendation tracking.
	"_pos": true, "_sid": true, "_ss": true, "_psq": true, "_fid": true, "_v": true,
	// Flipkart: per-visit and recommendation tracking. pid (product) and lid
	// (seller listing) identify what is sold, so they are kept.
	"pageuid": true, "fm": true, "hl_lid": true, "otracker": true, "otracker1": true,
	"ssid": true, "iid": true, "ppt": true, "ppn": true, "srno": true, "affid": true, "affextparam1": true,
	// Amazon: ad and recommendation tracking (the product is in the /dp/ path).
	"psc": true, "smid": true, "th": true, "source": true, "content-id": true, "crid": true,
	"sprefix": true, "qid": true, "sr": true, "keywords": true, "dib": true, "dib_tag": true,
}

var trackingPrefixes = []string{"utm_", "pf_rd_", "pd_rd_"}

// ListingURL cleans a product URL and derives the key used to recognise the
// same product page across users:
//
//	fetch: the URL to download (tracking parameters and #fragment removed)
//	key:   fetch, plus lowercase host without "www.", no scheme, no trailing
//	       slash, and remaining query parameters sorted
//
// Parameters that may change the price (e.g. Shopify's ?variant=) are kept.
func ListingURL(raw string) (fetch, key string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", errors.New("url must be a valid http(s) URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment, u.RawFragment = "", ""
	u.User = nil // never keep credentials embedded in a URL

	q := u.Query()
	for name := range q {
		lower := strings.ToLower(name)
		drop := trackingParams[lower]
		for _, p := range trackingPrefixes {
			drop = drop || strings.HasPrefix(lower, p)
		}
		if drop {
			q.Del(name)
		}
	}
	u.RawQuery = q.Encode() // Encode sorts parameters by name
	fetch = u.String()

	path := strings.TrimRight(u.EscapedPath(), "/")
	key = strings.TrimPrefix(u.Host, "www.") + path
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return fetch, key, nil
}
