// Package model holds the local read-model record types. All amounts are paise.
package model

import "time"

type Payment struct {
	ID          string
	OrderID     string
	Status      string // created, authorized, captured, failed, refunded
	Method      string // upi, card, netbanking, wallet
	AmountPaise int64
	Currency    string
	Captured    bool
	Email       string
	Contact     string
	ErrorCode   string
	ErrorReason string
	Refunded    int64 // amount already refunded, paise
	CreatedAt   time.Time
}

type Order struct {
	ID           string
	Status       string // created, attempted, paid
	AmountPaise  int64
	AmountPaid   int64
	Receipt      string
	CustomerName string
	CreatedAt    time.Time
}

type Invoice struct {
	ID           string
	Status       string // draft, issued, partially_paid, paid, expired
	AmountPaise  int64
	AmountPaid   int64
	CustomerName string
	CustomerMail string
	DueAt        time.Time
	CreatedAt    time.Time
}

type Settlement struct {
	ID          string
	Status      string // created, processed, failed
	AmountPaise int64
	FeesPaise   int64
	TaxPaise    int64
	UTR         string
	CreatedAt   time.Time
}
