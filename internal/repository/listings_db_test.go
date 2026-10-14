package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"pricepulse/internal/domain"
)

// These tests need a real PostgreSQL. They are skipped unless
// TEST_DATABASE_URL points at a database that may be written to, e.g.:
//
//	TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5433/pricepulse_test?sslmode=disable' go test ./internal/repository
//
// Never point it at your real database.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestLowestPriceAndDates(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewProductRepository(db)
	users := NewUserRepository(db)

	key := fmt.Sprintf("test.example/lowest-%d", time.Now().UnixNano())
	l, created, err := repo.FindOrCreateListing(ctx, "https://"+key, key)
	if err != nil || !created {
		t.Fatalf("create listing: %v created=%v", err, created)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM listings WHERE id = $1`, l.ID) })

	// A tracking user with target 92, so we can check the alert fan-out too.
	u, err := users.CreateUser(ctx, fmt.Sprintf("lowest-%d@example.com", time.Now().UnixNano()), "x", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	p, err := repo.Create(ctx, u.ID, 10, l.ID, "", "for testing", 92, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	day := func(d, hour int) time.Time { return time.Date(2026, 1, d, hour, 0, 0, 0, time.UTC) }
	steps := []struct {
		price     float64
		at        time.Time
		wantLow   float64
		wantDays  []time.Time
		wantHigh  float64
		situation string
	}{
		{100, day(1, 10), 100, []time.Time{day(1, 10)}, 100, "first price is the lowest"},
		{90, day(2, 9), 90, []time.Time{day(2, 9)}, 100, "new low replaces the dates"},
		{90, day(2, 18), 90, []time.Time{day(2, 9)}, 100, "same low, same day: not repeated"},
		{95, day(3, 9), 90, []time.Time{day(2, 9)}, 100, "higher price: lowest unchanged"},
		{90, day(4, 9), 90, []time.Time{day(2, 9), day(4, 9)}, 100, "same low on a new day: appended"},
		{120, day(5, 9), 90, []time.Time{day(2, 9), day(4, 9)}, 120, "new high"},
		{80, day(6, 9), 80, []time.Time{day(6, 9)}, 120, "new low resets the list"},
	}
	for _, s := range steps {
		if err := repo.RecordListingPrice(ctx, l.ID, s.price, "INR", "Test product", testDetails(), s.at); err != nil {
			t.Fatalf("%s: %v", s.situation, err)
		}
		got, err := repo.Listing(ctx, l.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.LowestPrice == nil || *got.LowestPrice != s.wantLow || got.HighestPrice == nil || *got.HighestPrice != s.wantHigh {
			t.Fatalf("%s: low=%v high=%v, want %v/%v", s.situation, deref(got.LowestPrice), deref(got.HighestPrice), s.wantLow, s.wantHigh)
		}
		if !sameTimes(got.LowestPriceDates, s.wantDays) {
			t.Fatalf("%s: dates=%v, want %v", s.situation, got.LowestPriceDates, s.wantDays)
		}
		if got.CurrentPrice != s.price || got.Currency != "INR" || got.Title != "Test product" {
			t.Fatalf("%s: listing not updated: %+v", s.situation, got)
		}
	}

	// The tracking product followed every price, and its name comes from the store.
	prod, err := repo.Owned(ctx, u.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prod.CurrentPrice != 80 || prod.Title != "Test product" || prod.LowestPrice == nil || *prod.LowestPrice != 80 {
		t.Fatalf("product not updated from listing: %+v", prod)
	}

	// Alert re-arm: mark sent, then a price above the target (92) clears it.
	repo.MarkAlertSent(ctx, p.ID, time.Now())
	repo.RecordListingPrice(ctx, l.ID, 99, "", "", domain.ListingDetails{}, day(7, 9))
	prod, _ = repo.Owned(ctx, u.ID, p.ID)
	if prod.AlertSentAt != nil {
		t.Fatal("price above target should re-arm the alert")
	}

	// Manual prices are private: in the owner's history, never in the public one.
	if _, err := repo.RecordManualPrice(ctx, u.ID, p.ID, 50, day(8, 9)); err != nil {
		t.Fatal(err)
	}
	public, _ := repo.PublicHistory(ctx, l.ID)
	mine, _ := repo.History(ctx, u.ID, p.ID)
	if len(mine) != len(public)+1 {
		t.Fatalf("history sizes: mine=%d public=%d", len(mine), len(public))
	}
	for _, h := range public {
		if h.Source != "scraper" || h.Price == 50 {
			t.Fatalf("manual price leaked into public history: %+v", h)
		}
	}
	got, _ := repo.Listing(ctx, l.ID)
	if *got.LowestPrice != 80 {
		t.Fatalf("manual price must not change the public lowest: %v", *got.LowestPrice)
	}

	// Tracking the same listing twice is refused.
	if _, err := repo.Create(ctx, u.ID, 10, l.ID, "", "", 50, time.Now()); err == nil {
		t.Fatal("duplicate tracking should fail")
	}

	// Details survive a later check that didn't include them.
	if got.ImageURL != "https://cdn.example/x.jpg" || got.Brand != "TestBrand" || got.MRP == nil || *got.MRP != 150 ||
		got.Rating == nil || *got.Rating != 4.5 || got.RatingCount == nil || *got.RatingCount != 12 || got.InStock == nil || !*got.InStock {
		t.Fatalf("details not kept: %+v", got.ListingDetails)
	}
	if prod.Note != "for testing" {
		t.Fatalf("note = %q", prod.Note)
	}

	// Bucket list: buying checks it off, frees the slot and stops alerts.
	if n, _ := repo.Count(ctx, u.ID); n != 1 {
		t.Fatalf("active count = %d", n)
	}
	b, err := repo.MarkBought(ctx, u.ID, p.ID, 85, day(9, 9))
	if err != nil || b.BoughtAt == nil || *b.BoughtPrice != 85 {
		t.Fatalf("mark bought: %+v %v", b, err)
	}
	if n, _ := repo.Count(ctx, u.ID); n != 0 {
		t.Fatalf("bought product should not use a slot, count = %d", n)
	}
	repo.RecordListingPrice(ctx, l.ID, 70, "", "", domain.ListingDetails{}, day(10, 9)) // below target 92
	due, _ := repo.AlertsDue(ctx, 100)
	for _, d := range due {
		if d.Product.ID == p.ID {
			t.Fatal("bought product must not get alerts")
		}
	}
	// Undo needs a free slot: with limit 0 it is refused, with 1 it works.
	if _, err := repo.UnmarkBought(ctx, u.ID, p.ID, 0); err == nil {
		t.Fatal("unmark should respect the limit")
	}
	if b, err := repo.UnmarkBought(ctx, u.ID, p.ID, 1); err != nil || b.BoughtAt != nil {
		t.Fatalf("unmark: %+v %v", b, err)
	}
}

func testDetails() domain.ListingDetails {
	rating, best, mrp := 4.5, 5.0, 150.0
	count, stock := 12, true
	return domain.ListingDetails{ImageURL: "https://cdn.example/x.jpg", Brand: "TestBrand",
		Rating: &rating, BestRating: &best, RatingCount: &count, MRP: &mrp, InStock: &stock}
}

func deref(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func sameTimes(a, b []time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}
