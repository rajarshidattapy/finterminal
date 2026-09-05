// Package fixtures seeds a deterministic, realistic test-mode dataset so the
// demo and the eval suite reproduce byte-for-byte on any machine, with no
// credentials. The shape is deliberate: a revenue drop in the trailing week,
// driven by a UPI failure spike clustered in a bank downtime window.
package fixtures

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/model"
	"github.com/rajarshidattapy/finterminal/internal/store"
)

const Seed = 20260905

var methods = []struct {
	name   string
	weight int
}{
	{"upi", 58}, {"card", 24}, {"netbanking", 11}, {"wallet", 7},
}

var customers = []struct {
	name, email, contact string
}{
	{"Ada Sharma", "ada@example.com", "+919876543210"},
	{"Sayan Roy", "sayan@example.com", "+919812345678"},
	{"Meera Iyer", "meera@example.com", "+919900112233"},
	{"Rahul Nair", "rahul@example.com", "+919845001122"},
	{"Priya Das", "priya@example.com", "+919733445566"},
	{"Vikram Rao", "vikram@example.com", "+919611223344"},
	{"Neha Gupta", "neha@example.com", "+919522334455"},
	{"Arjun Menon", "arjun@example.com", "+919433445566"},
}

type failMode struct{ code, reason string }

var upiFailures = []failMode{
	{"BAD_REQUEST_ERROR", "collect request expired"},
	{"GATEWAY_ERROR", "payer bank unavailable"},
	{"BAD_REQUEST_ERROR", "VPA does not exist"},
}

var otherFailures = []failMode{
	{"GATEWAY_ERROR", "issuing bank declined"},
	{"BAD_REQUEST_ERROR", "insufficient funds"},
	{"GATEWAY_ERROR", "authentication timed out"},
}

// Result reports what was written.
type Result struct {
	Payments, Orders, Invoices, Settlements int
	From, To                                time.Time
}

// Generate builds 90 days of history ending at `now` and writes it to the store.
func Generate(s *store.Store, now time.Time, days int) (*Result, error) {
	rnd := rand.New(rand.NewSource(Seed))
	start := now.AddDate(0, 0, -days).Truncate(24 * time.Hour)

	var payments []model.Payment
	var orders []model.Order
	var invoices []model.Invoice
	var settlements []model.Settlement

	seq := 0
	nextID := func(prefix string) string {
		seq++
		return fmt.Sprintf("%s%014d", prefix, 29000000000000+seq*7919)
	}

	for d := 0; d < days; d++ {
		day := start.AddDate(0, 0, d)
		daysAgo := days - d
		// Baseline volume with a weekly rhythm; the trailing 7 days run lighter.
		volume := 42 + rnd.Intn(12)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			volume -= 9
		}
		degraded := daysAgo <= 7
		if degraded {
			volume -= 5
		}

		for i := 0; i < volume; i++ {
			method := pickMethod(rnd)
			hour := pickHour(rnd, method, degraded)
			ts := day.Add(time.Duration(hour)*time.Hour +
				time.Duration(rnd.Intn(60))*time.Minute + time.Duration(rnd.Intn(60))*time.Second)
			if ts.After(now) {
				continue
			}
			cust := customers[rnd.Intn(len(customers))]
			amount := pickAmount(rnd)

			failProb := 0.088
			if method == "upi" {
				failProb = 0.095
				if degraded {
					// The trailing week's UPI failure spike.
					failProb = 0.185
				}
			}
			if degraded && method != "upi" {
				failProb = 0.10
			}

			p := model.Payment{
				ID:          nextID("pay_"),
				OrderID:     nextID("order_"),
				Method:      method,
				AmountPaise: amount,
				Currency:    "INR",
				Email:       cust.email,
				Contact:     cust.contact,
				CreatedAt:   ts,
			}
			switch {
			case rnd.Float64() < failProb:
				p.Status = "failed"
				var fm failMode
				if method == "upi" {
					// Weight the expired-collect and bank-downtime modes.
					switch {
					case degraded && hour >= 1 && hour < 4:
						fm = upiFailures[1]
					case rnd.Float64() < 0.55:
						fm = upiFailures[0]
					case rnd.Float64() < 0.7:
						fm = upiFailures[1]
					default:
						fm = upiFailures[2]
					}
				} else {
					fm = otherFailures[rnd.Intn(len(otherFailures))]
				}
				p.ErrorCode, p.ErrorReason = fm.code, fm.reason
			case rnd.Float64() < 0.02:
				p.Status = "authorized"
			default:
				p.Status = "captured"
				p.Captured = true
			}
			payments = append(payments, p)

			o := model.Order{
				ID:           p.OrderID,
				AmountPaise:  amount,
				Receipt:      fmt.Sprintf("rcpt_%05d", seq),
				CustomerName: cust.name,
				CreatedAt:    ts.Add(-90 * time.Second),
			}
			if p.Status == "captured" {
				o.Status, o.AmountPaid = "paid", amount
			} else {
				o.Status = "attempted"
			}
			orders = append(orders, o)
		}

		// A daily settlement, T+2, for processed volume.
		if d%1 == 0 && daysAgo > 2 {
			var gross int64
			for _, p := range payments {
				if p.Status == "captured" && sameDay(p.CreatedAt, day) {
					gross += p.AmountPaise
				}
			}
			if gross > 0 {
				fees := gross * 2 / 100
				tax := fees * 18 / 100
				settlements = append(settlements, model.Settlement{
					ID:          nextID("setl_"),
					Status:      "processed",
					AmountPaise: gross - fees - tax,
					FeesPaise:   fees,
					TaxPaise:    tax,
					UTR:         fmt.Sprintf("UTR%09d", 100000+seq),
					CreatedAt:   day.AddDate(0, 0, 2).Add(11 * time.Hour),
				})
			}
		}
	}

	// A handful of duplicate charges: same customer, same amount, minutes apart.
	for i := 0; i < 3; i++ {
		src := payments[len(payments)-1-i*37]
		dup := src
		dup.ID = nextID("pay_")
		dup.OrderID = nextID("order_")
		dup.Status = "captured"
		dup.Captured = true
		dup.ErrorCode, dup.ErrorReason = "", ""
		dup.CreatedAt = src.CreatedAt.Add(time.Duration(2+i) * time.Minute)
		payments = append(payments, dup)
	}

	// Open invoices, some overdue.
	for i := 0; i < 14; i++ {
		cust := customers[i%len(customers)]
		amt := int64((5 + rnd.Intn(45)) * 100000)
		issued := now.AddDate(0, 0, -(7 + rnd.Intn(50)))
		inv := model.Invoice{
			ID:           nextID("inv_"),
			Status:       "issued",
			AmountPaise:  amt,
			CustomerName: cust.name,
			CustomerMail: cust.email,
			CreatedAt:    issued,
			DueAt:        issued.AddDate(0, 0, 15),
		}
		if i%5 == 0 {
			inv.Status = "partially_paid"
			inv.AmountPaid = amt / 3
		}
		invoices = append(invoices, inv)
	}

	if err := s.UpsertPayments(payments); err != nil {
		return nil, err
	}
	if err := s.UpsertOrders(orders); err != nil {
		return nil, err
	}
	if err := s.UpsertInvoices(invoices); err != nil {
		return nil, err
	}
	if err := s.UpsertSettlements(settlements); err != nil {
		return nil, err
	}
	for _, e := range []string{"payments", "orders", "invoices", "settlements"} {
		if err := s.MarkSynced(e, now); err != nil {
			return nil, err
		}
	}
	return &Result{
		Payments: len(payments), Orders: len(orders),
		Invoices: len(invoices), Settlements: len(settlements),
		From: start, To: now,
	}, nil
}

func pickMethod(rnd *rand.Rand) string {
	total := 0
	for _, m := range methods {
		total += m.weight
	}
	n := rnd.Intn(total)
	for _, m := range methods {
		if n < m.weight {
			return m.name
		}
		n -= m.weight
	}
	return "upi"
}

// pickHour weights business hours, with a late-night tail that widens during
// the degraded week (the bank downtime window).
func pickHour(rnd *rand.Rand, method string, degraded bool) int {
	if degraded && method == "upi" && rnd.Float64() < 0.22 {
		return 1 + rnd.Intn(3)
	}
	if rnd.Float64() < 0.8 {
		return 9 + rnd.Intn(12)
	}
	return rnd.Intn(24)
}

func pickAmount(rnd *rand.Rand) int64 {
	switch r := rnd.Float64(); {
	case r < 0.55:
		return int64((3 + rnd.Intn(18)) * 10000) // ₹30 – ₹2,100
	case r < 0.9:
		return int64((25 + rnd.Intn(120)) * 10000) // ₹250 – ₹14,500
	default:
		return int64((150 + rnd.Intn(600)) * 10000) // ₹1,500 – ₹75,000
	}
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
