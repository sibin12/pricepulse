package domain

import "testing"

func TestListingURL(t *testing.T) {
	cases := []struct{ in, fetch, key string }{
		{
			"https://www.Boat-Lifestyle.com/products/boat-rockerz-uno/?utm_source=ig&utm_medium=social#reviews",
			"https://www.boat-lifestyle.com/products/boat-rockerz-uno/",
			"boat-lifestyle.com/products/boat-rockerz-uno",
		},
		{ // variant changes the price, so it is kept; tracking params go
			"https://shop.example.com/products/tee?variant=42&fbclid=abc&_pos=1&_sid=x&_ss=r",
			"https://shop.example.com/products/tee?variant=42",
			"shop.example.com/products/tee?variant=42",
		},
		{ // parameter order doesn't matter
			"http://example.com/item?b=2&a=1",
			"http://example.com/item?a=1&b=2",
			"example.com/item?a=1&b=2",
		},
		{ // Flipkart: per-visit/recommendation params dropped, product + seller kept
			"https://www.flipkart.com/jbl-flip-7/p/itm95744ea2c2dec?pid=ACCHERMQY8KSMYZH&lid=LSTACCHERMQY8KSMYZHWXUY0J&hl_lid=&marketplace=FLIPKART&fm=eyJ3dHAiOiJyZWNvIn0%3D&pageUID=1792000835115",
			"https://www.flipkart.com/jbl-flip-7/p/itm95744ea2c2dec?lid=LSTACCHERMQY8KSMYZHWXUY0J&marketplace=FLIPKART&pid=ACCHERMQY8KSMYZH",
			"flipkart.com/jbl-flip-7/p/itm95744ea2c2dec?lid=LSTACCHERMQY8KSMYZHWXUY0J&marketplace=FLIPKART&pid=ACCHERMQY8KSMYZH",
		},
		{ // credentials in the URL are dropped
			"https://user:pass@example.com/p",
			"https://example.com/p",
			"example.com/p",
		},
	}
	for _, c := range cases {
		fetch, key, err := ListingURL(c.in)
		if err != nil || fetch != c.fetch || key != c.key {
			t.Errorf("ListingURL(%q)\n got  %q %q %v\n want %q %q", c.in, fetch, key, err, c.fetch, c.key)
		}
	}

	// The same Flipkart product opened from two different pages -> same key.
	_, f1, _ := ListingURL("https://www.flipkart.com/x/p/itm1?pid=P1&lid=L1&pageUID=111&fm=aaa")
	_, f2, _ := ListingURL("https://www.flipkart.com/x/p/itm1?pid=P1&lid=L1&pageUID=222&otracker=search&ssid=q")
	if f1 != f2 {
		t.Errorf("flipkart keys differ: %q vs %q", f1, f2)
	}

	// Same product, different spellings -> same key.
	_, k1, _ := ListingURL("https://www.example.com/p/1/?utm_campaign=x")
	_, k2, _ := ListingURL("http://example.com/p/1")
	if k1 != k2 {
		t.Errorf("keys differ: %q vs %q", k1, k2)
	}
	for _, bad := range []string{"", "example.com/x", "ftp://example.com/x", "javascript:alert(1)"} {
		if _, _, err := ListingURL(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
