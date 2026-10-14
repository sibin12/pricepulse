package scraper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParsePrice(t *testing.T) {
	cases := map[string]float64{
		"19.99":          19.99,
		"$1,299.99":      1299.99,
		"1.299,99 €":     1299.99,
		"₹1,29,999":      129999,
		"₹1,29,999.50":   129999.5,
		"19,99":          19.99,
		"1,299":          1299,
		"1.299.000":      1299000,
		"1 299,00 €":     1299,
		"£51.77":         51.77,
		"From $10 – $20": 10,
		"USD 7":          7,
	}
	for in, want := range cases {
		got, err := parsePrice(in)
		if err != nil || got != want {
			t.Errorf("parsePrice(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "free", "0", "0.00", "N/A"} {
		if _, err := parsePrice(bad); err == nil {
			t.Errorf("parsePrice(%q) should fail", bad)
		}
	}
}

func TestExtract(t *testing.T) {
	cases := []struct {
		name, html string
		price      float64
		currency   string
		title      string
		source     string
	}{
		{
			name: "json-ld offer",
			html: `<html><head><title>Shop | Thing</title>
				<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product",
				"name":"Noise-cancelling Headphones","offers":{"@type":"Offer","price":"249.99","priceCurrency":"USD"}}</script>`,
			price: 249.99, currency: "USD", title: "Noise-cancelling Headphones", source: "json-ld",
		},
		{
			name: "json-ld graph with aggregate offer and numeric price",
			html: `<script type="application/ld+json">{"@graph":[{"@type":"WebPage"},
				{"@type":["Product"],"name":"Desk","offers":{"@type":"AggregateOffer","lowPrice":399,"priceCurrency":"EUR"}}]}</script>`,
			price: 399, currency: "EUR", title: "Desk", source: "json-ld",
		},
		{
			name: "json-ld offers array, broken blob ignored",
			html: `<script type="application/ld+json">{not json</script>
				<script type="application/ld+json">[{"@type":"Product","name":"Kettle","offers":[{"price":"35.50"}]}]</script>`,
			price: 35.5, title: "Kettle", source: "json-ld",
		},
		{
			name:  "open graph meta",
			html:  `<meta property="og:title" content="Blender"><meta property="product:price:amount" content="89.00"><meta property="product:price:currency" content="GBP">`,
			price: 89, currency: "GBP", title: "Blender", source: "meta",
		},
		{
			name:  "microdata text content",
			html:  `<title>Lamp</title><div itemscope><span itemprop="price">$42.10</span><meta itemprop="priceCurrency" content="USD"></div>`,
			price: 42.1, currency: "USD", title: "Lamp", source: "microdata",
		},
		{
			name:  "class heuristic skips old price and non-currency text",
			html:  `<title>Books</title><h1>A Light in the Attic</h1><p class="price-old">£60.00</p><span class="price-count">3 items</span><p class="price_color">£51.77</p>`,
			price: 51.77, title: "A Light in the Attic", source: "heuristic",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Extract(strings.NewReader(c.html))
			if err != nil {
				t.Fatalf("Extract error: %v", err)
			}
			if got.Price != c.price || got.Currency != c.currency || got.Title != c.title || got.Source != c.source {
				t.Errorf("got %+v; want price=%v currency=%q title=%q source=%q", got, c.price, c.currency, c.title, c.source)
			}
		})
	}
}

func TestExtractNoPrice(t *testing.T) {
	_, err := Extract(strings.NewReader(`<html><title>Nothing here</title><p>Contact us</p></html>`))
	if !errors.Is(err, ErrNoPrice) {
		t.Fatalf("want ErrNoPrice, got %v", err)
	}
}

func TestScrapeBlocksPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<meta property="product:price:amount" content="10">`))
	}))
	defer srv.Close()

	// Default options: loopback must be refused (SSRF protection).
	_, err := New(Options{}).Scrape(context.Background(), srv.URL)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("want ErrBlockedAddress, got %v", err)
	}

	// Explicitly allowed (tests/local dev): the scrape succeeds.
	res, err := New(Options{AllowPrivate: true}).Scrape(context.Background(), srv.URL)
	if err != nil || res.Price != 10 {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestScrapeReportsHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := New(Options{AllowPrivate: true}).Scrape(context.Background(), srv.URL)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden {
		t.Fatalf("want StatusError 403, got %v", err)
	}
}

func TestHostGateSpacesRequests(t *testing.T) {
	g := &hostGate{gap: 50 * time.Millisecond, next: map[string]time.Time{}}
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := g.wait(context.Background(), "shop.example"); err != nil {
			t.Fatal(err)
		}
	}
	// Three requests to one host need at least two gaps.
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Fatalf("requests not spaced: %v", el)
	}
	// A different host is not delayed.
	t0 := time.Now()
	g.wait(context.Background(), "other.example")
	if time.Since(t0) > 20*time.Millisecond {
		t.Fatal("other host was delayed")
	}
}

func TestExtractCleansStoreTitles(t *testing.T) {
	html := "<script type=\"application/ld+json\">{\"@type\":\"Product\",\"name\":\"JBL Flip 7, 16Hrs of Playtime,\uFFFD IP68\u200B Speaker\",\"offers\":{\"price\":\"9999\"}}</script>"
	got, err := Extract(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "JBL Flip 7, 16Hrs of Playtime, IP68 Speaker" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestExtractProductDetails(t *testing.T) {
	html := `<meta property="og:image" content="https://cdn.example/og.jpg">
	<script type="application/ld+json">{"@type":"Product","name":"Speaker",
	  "image":["//cdn.example/main.jpg","https://cdn.example/2.jpg"],
	  "brand":{"@type":"Brand","name":"JBL"},
	  "aggregateRating":{"ratingValue":4.3,"ratingCount":631,"reviewCount":72},
	  "offers":{"price":9999,"priceCurrency":"INR","availability":"https://schema.org/InStock",
	    "priceSpecification":[{"@type":"UnitPriceSpecification","priceType":"https://schema.org/ListPrice","price":16999}]}}</script>`
	got, err := Extract(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	if got.Price != 9999 || got.ListPrice != 16999 || got.Brand != "JBL" ||
		got.Image != "https://cdn.example/main.jpg" || got.Rating != 4.3 || got.BestRating != 5 ||
		got.RatingCount != 631 || got.InStock == nil || !*got.InStock {
		t.Fatalf("got %+v (in stock %v)", got, got.InStock)
	}
}

func TestExtractMRPFromPageStateAndFallbacks(t *testing.T) {
	// Flipkart-style: MRP only in inline page state; image only in og:image.
	html := `<meta property="og:image" content="https://cdn.example/og.jpg">
	<script>window.__STATE__={"pricing":{"finalPrice":9999,"mrp":16999}}</script>
	<script type="application/ld+json">{"@type":"Product","name":"Speaker","offers":{"price":"9999","availability":"OutOfStock"}}</script>`
	got, err := Extract(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	if got.ListPrice != 16999 || got.Image != "https://cdn.example/og.jpg" || got.InStock == nil || *got.InStock {
		t.Fatalf("got %+v", got)
	}

	// An "MRP" below the price, or a javascript: image, is rejected.
	html = `<script>var s={"mrp":50}</script>
	<script type="application/ld+json">{"@type":"Product","name":"X","image":"javascript:alert(1)","offers":{"price":100}}</script>`
	got, _ = Extract(strings.NewReader(html))
	if got.ListPrice != 0 || got.Image != "" || got.InStock != nil {
		t.Fatalf("bad values should be dropped: %+v", got)
	}
}

func TestExtractAnyVariantInStock(t *testing.T) {
	html := `<script type="application/ld+json">{"@type":"Product","name":"Tee","offers":[
	  {"price":"20","availability":"https://schema.org/OutOfStock"},
	  {"price":"20","availability":"https://schema.org/InStock"}]}</script>`
	got, err := Extract(strings.NewReader(html))
	if err != nil || got.InStock == nil || !*got.InStock {
		t.Fatalf("one variant in stock should mean in stock: %+v %v", got, err)
	}
}
