package scraper

import (
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Extract reads an HTML document and finds the product price using, in order
// of reliability:
//
//  1. JSON-LD structured data (schema.org Product/Offer) — what Google Shopping
//     reads, so most stores publish it.
//  2. Open Graph / product meta tags (product:price:amount, og:price:amount).
//  3. Microdata (itemprop="price").
//  4. A heuristic: the first element whose class contains "price" and whose
//     text looks like a price with a currency symbol.
//
// The structured sources are tried first because they are machine-readable and
// stable; the heuristic is a last resort and may be wrong on unusual pages.
func Extract(r io.Reader) (Result, error) {
	p := scanPage(r)
	res, err := extract(p)
	if err != nil {
		return res, err
	}
	res.Title = cleanText(res.Title)
	res.Brand = cleanText(res.Brand)
	res.Image = absoluteURL(first(res.Image, p.ogImage))
	// The page-state MRP is a fallback for stores without a schema.org list price.
	if res.ListPrice == 0 {
		res.ListPrice, _ = parsePrice(p.scriptMRP)
	}
	// Sanity check: an MRP is above the selling price and not absurdly so.
	// Anything else is more likely another product's number than a real MRP.
	if res.ListPrice <= res.Price || res.ListPrice > res.Price*10 {
		res.ListPrice = 0
	}
	if res.Rating <= 0 || res.BestRating <= 0 || res.Rating > res.BestRating {
		res.Rating, res.BestRating, res.RatingCount = 0, 0, 0
	}
	return res, nil
}

func extract(p page) (Result, error) {
	for _, blob := range p.jsonLD {
		if ld, ok := fromJSONLD(blob); ok {
			return Result{
				Price: ld.price, Currency: first(ld.currency, p.metaCurrency), Title: first(ld.name, p.ogTitle, p.title), Source: "json-ld",
				Image: ld.image, Brand: ld.brand, Rating: ld.rating, BestRating: ld.bestRating, RatingCount: ld.ratingCount,
				ListPrice: ld.listPrice, InStock: ld.inStock,
			}, nil
		}
	}
	if price, err := parsePrice(p.metaPrice); err == nil {
		return Result{Price: price, Currency: p.metaCurrency, Title: first(p.ogTitle, p.title), Source: "meta"}, nil
	}
	if price, err := parsePrice(p.itempropPrice); err == nil {
		return Result{Price: price, Currency: first(p.itempropCurrency, p.metaCurrency), Title: first(p.ogTitle, p.title), Source: "microdata"}, nil
	}
	if price, err := parsePrice(p.classPrice); err == nil {
		return Result{Price: price, Currency: p.metaCurrency, Title: first(p.ogTitle, p.h1, p.title), Source: "heuristic"}, nil
	}
	return Result{}, ErrNoPrice
}

// page collects every candidate value in one pass over the HTML.
type page struct {
	jsonLD           []string
	ogImage          string
	scriptMRP        string // "mrp" value from the store's inline page data (Indian stores)
	metaPrice        string
	metaCurrency     string
	itempropPrice    string
	itempropCurrency string
	classPrice       string
	ogTitle          string
	title            string
	h1               string
}

// scanPage walks the HTML with a streaming tokenizer (no full DOM in memory).
func scanPage(r io.Reader) page {
	var p page
	z := html.NewTokenizer(r)

	// capture tells the next text token where to go, for elements like
	// <title>…</title> whose value is their text content.
	var capture *string
	var captureBuf strings.Builder
	var captureTag string
	var classPriceCandidate bool

	for {
		switch z.Next() {
		case html.ErrorToken:
			// io.EOF (end of document) or a read error; either way we're done.
			return p

		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			attrs := attrMap(tok.Attr)

			switch tok.Data {
			case "script":
				// The tokenizer returns a <script> body as one raw text token.
				isLD := strings.Contains(strings.ToLower(attrs["type"]), "ld+json")
				if z.Next() == html.TextToken {
					if isLD {
						p.jsonLD = append(p.jsonLD, string(z.Text()))
					} else if p.scriptMRP == "" {
						// Indian stores (e.g. Flipkart) keep the MRP only in their
						// inline page state, as "mrp":16999.
						if m := mrpPattern.FindSubmatch(z.Text()); m != nil {
							p.scriptMRP = string(m[1])
						}
					}
				}
				continue
			case "meta":
				key := strings.ToLower(first(attrs["property"], attrs["name"], attrs["itemprop"]))
				content := strings.TrimSpace(attrs["content"])
				switch key {
				case "product:price:amount", "og:price:amount":
					setOnce(&p.metaPrice, content)
				case "product:price:currency", "og:price:currency":
					setOnce(&p.metaCurrency, content)
				case "og:title":
					setOnce(&p.ogTitle, content)
				case "og:image", "og:image:secure_url":
					setOnce(&p.ogImage, content)
				}
			}

			// Microdata: <span itemprop="price" content="19.99"> or text content.
			switch attrs["itemprop"] {
			case "price":
				if c := attrs["content"]; c != "" {
					setOnce(&p.itempropPrice, c)
				} else if p.itempropPrice == "" && tok.Type == html.StartTagToken {
					capture, captureTag = &p.itempropPrice, tok.Data
				}
			case "priceCurrency":
				setOnce(&p.itempropCurrency, attrs["content"])
			}

			if capture == nil && tok.Type == html.StartTagToken {
				switch {
				case tok.Data == "title" && p.title == "":
					capture, captureTag = &p.title, "title"
				case tok.Data == "h1" && p.h1 == "":
					capture, captureTag = &p.h1, "h1"
				case p.classPrice == "" && isPriceClass(attrs["class"]) && tok.Data != "meta":
					capture, captureTag, classPriceCandidate = &p.classPrice, tok.Data, true
				}
			}

		case html.TextToken:
			if capture != nil {
				captureBuf.Write(z.Text())
			}

		case html.EndTagToken:
			if capture != nil {
				name, _ := z.TagName()
				if string(name) == captureTag {
					text := collapseSpace(captureBuf.String())
					// The class heuristic only accepts text that carries a currency
					// marker, to avoid grabbing quantities or ratings.
					if !classPriceCandidate || hasCurrencyMarker(text) {
						setOnce(capture, text)
					}
					capture, captureTag, classPriceCandidate = nil, "", false
					captureBuf.Reset()
				}
			}
		}
	}
}

// fromJSONLD searches one JSON-LD blob for a schema.org Product with a price.
// ldProduct is what we read from a schema.org Product.
type ldProduct struct {
	price, listPrice, rating, bestRating float64
	ratingCount                          int
	currency, name, image, brand         string
	inStock                              *bool
}

// fromJSONLD searches one JSON-LD blob for a schema.org Product with a price.
func fromJSONLD(blob string) (ldProduct, bool) {
	var v any
	var out ldProduct
	if err := json.Unmarshal([]byte(strings.TrimSpace(blob)), &v); err != nil {
		return out, false // malformed JSON-LD is common; just skip it
	}
	var found bool
	walkJSON(v, 0, func(m map[string]any) bool {
		if !hasType(m["@type"], "Product", "ProductGroup", "IndividualProduct") {
			return false
		}
		o, ok := offerInfo(m["offers"])
		if !ok {
			return false
		}
		found = true
		out.price, out.currency, out.listPrice, out.inStock = o.price, o.currency, o.listPrice, o.inStock
		out.name = collapseSpace(str(m["name"]))
		out.image = imageURL(m["image"])
		out.brand = nameOf(m["brand"])
		if r, ok := m["aggregateRating"].(map[string]any); ok {
			out.rating, _ = strconv.ParseFloat(str(r["ratingValue"]), 64)
			out.bestRating = 5 // schema.org's default scale
			if b, err := strconv.ParseFloat(str(r["bestRating"]), 64); err == nil && b > 0 {
				out.bestRating = b
			}
			count, _ := strconv.Atoi(first(str(r["ratingCount"]), str(r["reviewCount"])))
			out.ratingCount = count
		}
		return true
	})
	return out, found
}

type offer struct {
	price, listPrice float64
	currency         string
	inStock          *bool
}

// offerInfo handles the shapes schema.org allows for "offers": a single Offer,
// an array of Offers, or an AggregateOffer with lowPrice. It also reads the
// list price (MRP) from a priceSpecification marked ListPrice/Strikethrough,
// and availability (InStock, OutOfStock, ...).
func offerInfo(v any) (offer, bool) {
	switch o := v.(type) {
	case []any:
		// One offer per variant (e.g. colours on Shopify): use the first
		// priced offer, and call the product in stock if ANY variant is.
		var result offer
		var found bool
		for _, item := range o {
			off, ok := offerInfo(item)
			if !ok {
				continue
			}
			if !found {
				result, found = off, true
			}
			if off.inStock != nil && (*off.inStock || result.inStock == nil) {
				result.inStock = off.inStock
			}
		}
		if found {
			return result, true
		}
	case map[string]any:
		out := offer{currency: str(o["priceCurrency"]), inStock: availability(str(o["availability"]))}
		for _, spec := range asList(o["priceSpecification"]) {
			sm, ok := spec.(map[string]any)
			if !ok {
				continue
			}
			kind := strings.ToLower(str(sm["priceType"]))
			if strings.Contains(kind, "listprice") || strings.Contains(kind, "strikethrough") || strings.Contains(kind, "msrp") {
				out.listPrice, _ = parsePrice(str(sm["price"]))
			} else if out.price == 0 {
				out.price, _ = parsePrice(str(sm["price"]))
				out.currency = first(out.currency, str(sm["priceCurrency"]))
			}
		}
		for _, key := range []string{"price", "lowPrice"} {
			if p, err := parsePrice(str(o[key])); err == nil {
				out.price = p
				break
			}
		}
		if out.price > 0 {
			return out, true
		}
	}
	return offer{}, false
}

// availability maps schema.org availability URLs to in stock / out of stock.
func availability(s string) *bool {
	s = strings.ToLower(s)
	var v bool
	switch {
	case strings.Contains(s, "instock"), strings.Contains(s, "limitedavailability"), strings.Contains(s, "onlineonly"):
		v = true
	case strings.Contains(s, "outofstock"), strings.Contains(s, "soldout"), strings.Contains(s, "discontinued"):
		v = false
	default:
		return nil
	}
	return &v
}

// imageURL reads schema.org "image": a URL string, a list, or an ImageObject.
func imageURL(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		for _, item := range t {
			if u := imageURL(item); u != "" {
				return u
			}
		}
	case map[string]any:
		return first(str(t["url"]), str(t["contentUrl"]))
	}
	return ""
}

// nameOf reads a "brand"-style value: a plain string or an object with "name".
func nameOf(v any) string {
	if m, ok := v.(map[string]any); ok {
		return str(m["name"])
	}
	return str(v)
}

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	if v == nil {
		return nil
	}
	return []any{v}
}

// absoluteURL accepts only http(s) URLs ("//cdn..." becomes https). Anything
// else (javascript:, data:, relative paths) is dropped, because the URL ends
// up in an <img src> in the browser.
func absoluteURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return u
	}
	return ""
}

var mrpPattern = regexp.MustCompile(`"mrp"\s*:\s*"?(\d+(?:\.\d+)?)`)

// walkJSON visits every object in a JSON tree (depth-limited) until visit
// returns true. This finds Products wherever they sit: top level, @graph,
// arrays, or nested under mainEntity.
func walkJSON(v any, depth int, visit func(map[string]any) bool) bool {
	if depth > 12 {
		return false
	}
	switch t := v.(type) {
	case map[string]any:
		if visit(t) {
			return true
		}
		for _, child := range t {
			if walkJSON(child, depth+1, visit) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if walkJSON(child, depth+1, visit) {
				return true
			}
		}
	}
	return false
}

// hasType matches "@type" whether it is a string or array, with or without
// a "http://schema.org/" prefix.
func hasType(v any, want ...string) bool {
	var types []string
	switch t := v.(type) {
	case string:
		types = []string{t}
	case []any:
		for _, x := range t {
			types = append(types, str(x))
		}
	}
	for _, t := range types {
		t = t[strings.LastIndex(t, "/")+1:]
		for _, w := range want {
			if strings.EqualFold(t, w) {
				return true
			}
		}
	}
	return false
}

// --- small helpers ---

func attrMap(attrs []html.Attribute) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[strings.ToLower(a.Key)] = a.Val
	}
	return m
}

// str converts a JSON value (string or number) to a string.
func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

func first(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func setOnce(dst *string, v string) {
	if *dst == "" {
		*dst = strings.TrimSpace(v)
	}
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// cleanText removes characters that only appear because of a store's own
// data errors: the Unicode replacement character "\uFFFD" (shown as a box or
// "?" for broken bytes), zero-width spaces and control characters. For
// example Flipkart serves "Playtime,\uFFFD IP68".
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\uFFFD', r == '\u200B', r == '\u200C', r == '\u200D', r == '\uFEFF':
			return -1 // drop
		case r < ' ' || (r >= 0x7f && r < 0xa0):
			return ' '
		}
		return r
	}, s)
	return collapseSpace(s)
}

// isPriceClass matches class names like "price", "price_color", "product-price"
// but not "price-old"/"was-price" style strikethrough prices.
func isPriceClass(class string) bool {
	c := strings.ToLower(class)
	if !strings.Contains(c, "price") {
		return false
	}
	for _, bad := range []string{"old", "was", "strike", "compare", "regular", "original", "range", "filter"} {
		if strings.Contains(c, bad) {
			return false
		}
	}
	return true
}

func hasCurrencyMarker(s string) bool {
	return strings.ContainsAny(s, "$€£¥₹₩₽₺₫฿") ||
		strings.Contains(strings.ToUpper(s), "USD") || strings.Contains(strings.ToUpper(s), "EUR") ||
		strings.Contains(strings.ToUpper(s), "GBP") || strings.Contains(strings.ToUpper(s), "INR") ||
		strings.Contains(s, "Rs")
}
