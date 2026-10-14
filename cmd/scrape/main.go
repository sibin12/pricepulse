// Command scrape is a debugging tool: it runs the same scraper the API uses
// against one or more URLs and prints what it found, without touching the DB.
//
//	go run ./cmd/scrape https://store.example/product/123
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"pricepulse/internal/scraper"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: scrape <url> [url...]")
		os.Exit(2)
	}

	s := scraper.New(scraper.Options{})
	failed := false
	for _, u := range os.Args[1:] {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		start := time.Now()
		res, err := s.Scrape(ctx, u)
		cancel()

		if err != nil {
			failed = true
			fmt.Printf("FAIL %s\n     %v\n", u, err)
			continue
		}
		fmt.Printf("OK   %s\n     price=%.2f currency=%q source=%s (%s)\n     title=%q\n",
			u, res.Price, res.Currency, res.Source, time.Since(start).Round(time.Millisecond), res.Title)
		stock := "unknown"
		if res.InStock != nil {
			stock = fmt.Sprint(*res.InStock)
		}
		fmt.Printf("     mrp=%.2f brand=%q rating=%.1f/%.0f (%d) in_stock=%s\n     image=%q\n",
			res.ListPrice, res.Brand, res.Rating, res.BestRating, res.RatingCount, stock, res.Image)
	}
	if failed {
		os.Exit(1)
	}
}
