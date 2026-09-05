// Package store is the local read model: a SQLite mirror of the Razorpay
// account, synced incrementally on a created_at watermark. Reads may use it;
// the write plane never does.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB   *sql.DB
	Path string
}

const schema = `
CREATE TABLE IF NOT EXISTS payments (
  id TEXT PRIMARY KEY, order_id TEXT, status TEXT, method TEXT,
  amount_paise INTEGER NOT NULL, currency TEXT, captured INTEGER,
  email TEXT, contact TEXT, error_code TEXT, error_reason TEXT,
  refunded_paise INTEGER DEFAULT 0, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_pay_created ON payments(created_at);
CREATE INDEX IF NOT EXISTS idx_pay_status ON payments(status);

CREATE TABLE IF NOT EXISTS orders (
  id TEXT PRIMARY KEY, status TEXT, amount_paise INTEGER NOT NULL,
  amount_paid INTEGER, receipt TEXT, customer_name TEXT, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_ord_created ON orders(created_at);

CREATE TABLE IF NOT EXISTS invoices (
  id TEXT PRIMARY KEY, status TEXT, amount_paise INTEGER NOT NULL,
  amount_paid INTEGER, customer_name TEXT, customer_mail TEXT,
  due_at INTEGER, created_at INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS settlements (
  id TEXT PRIMARY KEY, status TEXT, amount_paise INTEGER NOT NULL,
  fees_paise INTEGER, tax_paise INTEGER, utr TEXT, created_at INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS sync_state (
  entity TEXT PRIMARY KEY, watermark INTEGER NOT NULL, synced_at INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS idempotency (
  key TEXT PRIMARY KEY, capability TEXT, params TEXT,
  response_id TEXT, created_at INTEGER NOT NULL);
`

// DefaultPath is ~/.razorpay/cache.db.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "cache.db"
	}
	return filepath.Join(home, ".razorpay", "cache.db")
}

func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{DB: db, Path: path}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) UpsertPayments(ps []model.Payment) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare(`INSERT INTO payments
      (id,order_id,status,method,amount_paise,currency,captured,email,contact,error_code,error_reason,refunded_paise,created_at)
      VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
      ON CONFLICT(id) DO UPDATE SET status=excluded.status, captured=excluded.captured,
        refunded_paise=excluded.refunded_paise, error_code=excluded.error_code, error_reason=excluded.error_reason`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, p := range ps {
		if _, err := st.Exec(p.ID, p.OrderID, p.Status, p.Method, p.AmountPaise, p.Currency,
			p.Captured, p.Email, p.Contact, p.ErrorCode, p.ErrorReason, p.Refunded, p.CreatedAt.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertOrders(os_ []model.Order) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare(`INSERT INTO orders (id,status,amount_paise,amount_paid,receipt,customer_name,created_at)
      VALUES (?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status, amount_paid=excluded.amount_paid`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, o := range os_ {
		if _, err := st.Exec(o.ID, o.Status, o.AmountPaise, o.AmountPaid, o.Receipt, o.CustomerName, o.CreatedAt.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertInvoices(is []model.Invoice) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare(`INSERT INTO invoices (id,status,amount_paise,amount_paid,customer_name,customer_mail,due_at,created_at)
      VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status, amount_paid=excluded.amount_paid`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, i := range is {
		if _, err := st.Exec(i.ID, i.Status, i.AmountPaise, i.AmountPaid, i.CustomerName, i.CustomerMail,
			i.DueAt.Unix(), i.CreatedAt.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertSettlements(ss []model.Settlement) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare(`INSERT INTO settlements (id,status,amount_paise,fees_paise,tax_paise,utr,created_at)
      VALUES (?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, x := range ss {
		if _, err := st.Exec(x.ID, x.Status, x.AmountPaise, x.FeesPaise, x.TaxPaise, x.UTR, x.CreatedAt.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkSynced records the watermark for an entity.
func (s *Store) MarkSynced(entity string, watermark time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO sync_state (entity,watermark,synced_at) VALUES (?,?,?)
      ON CONFLICT(entity) DO UPDATE SET watermark=excluded.watermark, synced_at=excluded.synced_at`,
		entity, watermark.Unix(), time.Now().Unix())
	return err
}

// LastSync returns when the mirror was last refreshed, zero if never.
func (s *Store) LastSync() time.Time {
	var ts sql.NullInt64
	_ = s.DB.QueryRow(`SELECT MAX(synced_at) FROM sync_state`).Scan(&ts)
	if !ts.Valid {
		return time.Time{}
	}
	return time.Unix(ts.Int64, 0)
}

// Watermark returns the newest created_at seen for an entity.
func (s *Store) Watermark(entity string) time.Time {
	var ts sql.NullInt64
	_ = s.DB.QueryRow(`SELECT watermark FROM sync_state WHERE entity=?`, entity).Scan(&ts)
	if !ts.Valid {
		return time.Time{}
	}
	return time.Unix(ts.Int64, 0)
}

func (s *Store) CountPayments() int {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM payments`).Scan(&n)
	return n
}

func (s *Store) CountOrders() int {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n)
	return n
}

// ReserveIdempotencyKey persists a key before the call is made.
func (s *Store) ReserveIdempotencyKey(key, capability, params string) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO idempotency (key,capability,params,created_at) VALUES (?,?,?,?)`,
		key, capability, params, time.Now().Unix())
	return err
}

// LookupIdempotency returns a previously recorded response id, if any.
func (s *Store) LookupIdempotency(key string) (string, bool) {
	var id sql.NullString
	if err := s.DB.QueryRow(`SELECT response_id FROM idempotency WHERE key=?`, key).Scan(&id); err != nil {
		return "", false
	}
	return id.String, id.Valid && id.String != ""
}

func (s *Store) RecordIdempotencyResult(key, responseID string) error {
	_, err := s.DB.Exec(`UPDATE idempotency SET response_id=? WHERE key=?`, responseID, key)
	return err
}
