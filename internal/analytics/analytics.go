// Package analytics computes every number this program displays. The model
// never calculates: it receives the values in this package's FactSet and
// restates them. All arithmetic here is exact integer arithmetic over paise.
package analytics

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/money"
	"github.com/rajarshidattapy/finterminal/internal/store"
)

// Window is a half-open [From, To) time range.
type Window struct {
	From time.Time
	To   time.Time
}

func (w Window) Dur() time.Duration { return w.To.Sub(w.From) }

// Previous returns the equally sized window immediately before w.
func (w Window) Previous() Window {
	d := w.Dur()
	return Window{From: w.From.Add(-d), To: w.From}
}

func (w Window) Label() string {
	return fmt.Sprintf("%s – %s", w.From.Format("2 Jan"), w.To.Add(-time.Second).Format("2 Jan"))
}

// Fact is one computed value. Display is the exact string the narrator is
// allowed to repeat; nothing else may appear as a figure in the output.
type Fact struct {
	Key     string  `json:"key"`
	Display string  `json:"display"`
	Raw     float64 `json:"raw"`
}

// Row is a line in a computed table.
type Row struct {
	Label  string `json:"label"`
	Count  int    `json:"count"`
	Amount int64  `json:"amount_paise,omitempty"`
	Extra  string `json:"extra,omitempty"`
}

// FactSet is the complete, closed set of values a narration may contain.
type FactSet struct {
	Capability string            `json:"capability"`
	Window     Window            `json:"-"`
	WindowFrom string            `json:"window_from"`
	WindowTo   string            `json:"window_to"`
	CompareTo  string            `json:"compare_to,omitempty"`
	Facts      []Fact            `json:"facts"`
	Tables     map[string][]Row  `json:"tables,omitempty"`
	Notes      []string          `json:"notes,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
	Rows       int               `json:"rows_scanned"`
	Queries    int               `json:"queries"`
	QueryMS    int64             `json:"query_ms"`
}

func (fs *FactSet) add(key, display string, raw float64) {
	fs.Facts = append(fs.Facts, Fact{Key: key, Display: display, Raw: raw})
}

// Get returns the display string for a fact key.
func (fs *FactSet) Get(key string) string {
	for _, f := range fs.Facts {
		if f.Key == key {
			return f.Display
		}
	}
	return ""
}

// Num returns the raw value for a fact key.
func (fs *FactSet) Num(key string) float64 {
	for _, f := range fs.Facts {
		if f.Key == key {
			return f.Raw
		}
	}
	return 0
}

type Engine struct {
	S       *store.Store
	queries int
	elapsed time.Duration
}

func New(s *store.Store) *Engine { return &Engine{S: s} }

func (e *Engine) query(q string, args ...any) (*sql.Rows, error) {
	t0 := time.Now()
	rows, err := e.S.DB.Query(q, args...)
	e.elapsed += time.Since(t0)
	e.queries++
	return rows, err
}

func (e *Engine) row(q string, args ...any) *sql.Row {
	t0 := time.Now()
	r := e.S.DB.QueryRow(q, args...)
	e.elapsed += time.Since(t0)
	e.queries++
	return r
}

func (e *Engine) stamp(fs *FactSet, w Window) {
	fs.WindowFrom = w.From.Format("2006-01-02")
	fs.WindowTo = w.To.Add(-time.Second).Format("2006-01-02")
	fs.Window = w
	fs.Queries = e.queries
	fs.QueryMS = e.elapsed.Milliseconds()
}

type periodTotals struct {
	Captured  int64
	Count     int
	Attempted int
	Failed    int
}

func (e *Engine) totals(w Window) (periodTotals, error) {
	var t periodTotals
	err := e.row(`SELECT
        COALESCE(SUM(CASE WHEN status='captured' THEN amount_paise ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN status='captured' THEN 1 ELSE 0 END),0),
        COUNT(*),
        COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0)
      FROM payments WHERE created_at >= ? AND created_at < ?`,
		w.From.Unix(), w.To.Unix()).Scan(&t.Captured, &t.Count, &t.Attempted, &t.Failed)
	return t, err
}

// RevenueBreakdown answers "why did revenue drop this week".
func (e *Engine) RevenueBreakdown(w Window, compare bool) (*FactSet, error) {
	fs := &FactSet{Capability: "revenue_breakdown", Tables: map[string][]Row{}}
	cur, err := e.totals(w)
	if err != nil {
		return nil, err
	}
	curSR := money.Pct(int64(cur.Count), int64(cur.Attempted))
	fs.add("current_revenue", money.FormatShort(cur.Captured), float64(cur.Captured))
	fs.add("current_count", fmt.Sprintf("%d", cur.Count), float64(cur.Count))
	fs.add("current_success_rate", fmt.Sprintf("%.1f%%", curSR), curSR)
	fs.Meta = map[string]string{"current_label": w.Label()}
	fs.Rows = cur.Attempted

	if compare {
		pw := w.Previous()
		prev, err := e.totals(pw)
		if err != nil {
			return nil, err
		}
		fs.CompareTo = "previous_period"
		fs.Meta["previous_label"] = pw.Label()
		fs.add("previous_revenue", money.FormatShort(prev.Captured), float64(prev.Captured))
		fs.add("previous_count", fmt.Sprintf("%d", prev.Count), float64(prev.Count))
		prevSR := money.Pct(int64(prev.Count), int64(prev.Attempted))
		fs.add("previous_success_rate", fmt.Sprintf("%.1f%%", prevSR), prevSR)

		delta := cur.Captured - prev.Captured
		fs.add("delta_revenue", money.FormatShort(delta), float64(delta))
		pct := 0.0
		if prev.Captured != 0 {
			pct = float64(delta) * 100 / float64(prev.Captured)
		}
		fs.add("delta_pct", fmt.Sprintf("%.1f%%", abs(pct)), pct)
		fs.add("delta_direction", direction(delta), float64(sign(delta)))
		srDelta := curSR - prevSR
		fs.add("success_rate_delta_pp", fmt.Sprintf("%.1fpp", abs(srDelta)), srDelta)
		fs.add("previous_failed", fmt.Sprintf("%d", prev.Failed), float64(prev.Failed))
		fs.add("current_failed", fmt.Sprintf("%d", cur.Failed), float64(cur.Failed))
		fs.Rows += prev.Attempted
	}

	// Method mix for the current window.
	rows, err := e.query(`SELECT method,
        COALESCE(SUM(CASE WHEN status='captured' THEN amount_paise ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN status='captured' THEN 1 ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0)
      FROM payments WHERE created_at >= ? AND created_at < ?
      GROUP BY method ORDER BY 2 DESC`, w.From.Unix(), w.To.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var amt int64
		var ok, failed int
		if err := rows.Scan(&m, &amt, &ok, &failed); err != nil {
			return nil, err
		}
		fs.Tables["by_method"] = append(fs.Tables["by_method"], Row{
			Label: m, Count: ok, Amount: amt, Extra: fmt.Sprintf("%d failed", failed)})
	}

	// Unpaid high-value orders in the window.
	var unpaidCount int
	var unpaidAmt int64
	_ = e.row(`SELECT COUNT(*), COALESCE(SUM(amount_paise),0) FROM orders
        WHERE created_at >= ? AND created_at < ? AND status != 'paid' AND amount_paise >= 1000000`,
		w.From.Unix(), w.To.Unix()).Scan(&unpaidCount, &unpaidAmt)
	fs.add("unpaid_highvalue_count", fmt.Sprintf("%d", unpaidCount), float64(unpaidCount))
	fs.add("unpaid_highvalue_amount", money.FormatShort(unpaidAmt), float64(unpaidAmt))

	e.stamp(fs, w)
	return fs, nil
}

// SuccessRate groups attempted vs captured by method.
func (e *Engine) SuccessRate(w Window, method string) (*FactSet, error) {
	fs := &FactSet{Capability: "payment_success_rate", Tables: map[string][]Row{}}
	q := `SELECT method, COUNT(*), COALESCE(SUM(CASE WHEN status='captured' THEN 1 ELSE 0 END),0)
        FROM payments WHERE created_at >= ? AND created_at < ?`
	args := []any{w.From.Unix(), w.To.Unix()}
	if method != "" {
		q += ` AND method = ?`
		args = append(args, method)
	}
	q += ` GROUP BY method ORDER BY 2 DESC`
	rows, err := e.query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var totAtt, totOK int
	for rows.Next() {
		var m string
		var att, ok int
		if err := rows.Scan(&m, &att, &ok); err != nil {
			return nil, err
		}
		totAtt += att
		totOK += ok
		fs.Tables["by_method"] = append(fs.Tables["by_method"], Row{
			Label: m, Count: att, Extra: fmt.Sprintf("%.1f%%", money.Pct(int64(ok), int64(att)))})
	}
	overall := money.Pct(int64(totOK), int64(totAtt))
	fs.add("overall_success_rate", fmt.Sprintf("%.1f%%", overall), overall)
	fs.add("attempted", fmt.Sprintf("%d", totAtt), float64(totAtt))
	fs.add("captured", fmt.Sprintf("%d", totOK), float64(totOK))
	if method != "" {
		fs.Meta = map[string]string{"method": method}
	}
	fs.Rows = totAtt
	e.stamp(fs, w)
	return fs, nil
}

// FailureAnalysis groups failures on error_code/error_reason and flags any
// clustering by hour of day.
func (e *Engine) FailureAnalysis(w Window, method string) (*FactSet, error) {
	fs := &FactSet{Capability: "failure_analysis", Tables: map[string][]Row{}}
	q := `SELECT error_code, error_reason, COUNT(*) FROM payments
        WHERE status='failed' AND created_at >= ? AND created_at < ?`
	args := []any{w.From.Unix(), w.To.Unix()}
	if method != "" {
		q += ` AND method = ?`
		args = append(args, method)
	}
	q += ` GROUP BY error_code, error_reason ORDER BY 3 DESC`
	rows, err := e.query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type fail struct {
		code, reason string
		n            int
	}
	var fails []fail
	total := 0
	for rows.Next() {
		var f fail
		if err := rows.Scan(&f.code, &f.reason, &f.n); err != nil {
			return nil, err
		}
		fails = append(fails, f)
		total += f.n
	}
	for _, f := range fails {
		fs.Tables["patterns"] = append(fs.Tables["patterns"], Row{
			Label: f.code + " / " + f.reason, Count: f.n,
			Extra: fmt.Sprintf("%.0f%%", money.Pct(int64(f.n), int64(total)))})
	}
	fs.add("failed_total", fmt.Sprintf("%d", total), float64(total))
	if method != "" {
		fs.Meta = map[string]string{"method": method}
	}

	// Hour-of-day clustering: report the 4-hour band holding the most failures.
	hrows, err := e.query(`SELECT CAST(strftime('%H', created_at, 'unixepoch', '+5 hours', '+30 minutes') AS INTEGER), COUNT(*)
        FROM payments WHERE status='failed' AND created_at >= ? AND created_at < ? GROUP BY 1`,
		w.From.Unix(), w.To.Unix())
	if err == nil {
		defer hrows.Close()
		byHour := make([]int, 24)
		for hrows.Next() {
			var h, n int
			if err := hrows.Scan(&h, &n); err == nil && h >= 0 && h < 24 {
				byHour[h] = n
			}
		}
		bestStart, best := 0, -1
		for s := 0; s < 24; s++ {
			sum := 0
			for i := 0; i < 4; i++ {
				sum += byHour[(s+i)%24]
			}
			if sum > best {
				best, bestStart = sum, s
			}
		}
		if total > 0 && float64(best)/float64(total) >= 0.35 {
			fs.add("peak_band_count", fmt.Sprintf("%d", best), float64(best))
			fs.Notes = append(fs.Notes, fmt.Sprintf("%d of %d failures fall in %02d:00-%02d:00 IST",
				best, total, bestStart, (bestStart+4)%24))
		}
	}
	fs.Rows = total
	e.stamp(fs, w)
	return fs, nil
}

// ListPayments returns matching payments, newest first.
func (e *Engine) ListPayments(w Window, status, method string, minPaise int64, limit int) (*FactSet, error) {
	fs := &FactSet{Capability: "list_payments", Tables: map[string][]Row{}}
	q := `SELECT id, status, method, amount_paise, COALESCE(email,''), created_at FROM payments
        WHERE created_at >= ? AND created_at < ?`
	args := []any{w.From.Unix(), w.To.Unix()}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if method != "" {
		q += ` AND method = ?`
		args = append(args, method)
	}
	if minPaise > 0 {
		q += ` AND amount_paise >= ?`
		args = append(args, minPaise)
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := e.query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var total int64
	n := 0
	for rows.Next() {
		var id, st, m, email string
		var amt, ts int64
		if err := rows.Scan(&id, &st, &m, &amt, &email, &ts); err != nil {
			return nil, err
		}
		n++
		total += amt
		fs.Tables["payments"] = append(fs.Tables["payments"], Row{
			Label: id, Amount: amt,
			Extra: fmt.Sprintf("%s · %s · %s", st, m, time.Unix(ts, 0).Format("2 Jan 15:04"))})
	}
	fs.add("result_count", fmt.Sprintf("%d", n), float64(n))
	fs.add("result_total", money.FormatShort(total), float64(total))
	fs.Rows = n
	e.stamp(fs, w)
	return fs, nil
}

// UnpaidInvoices lists issued/partially paid invoices with an outstanding balance.
func (e *Engine) UnpaidInvoices(limit int) (*FactSet, error) {
	fs := &FactSet{Capability: "unpaid_invoices", Tables: map[string][]Row{}}
	if limit <= 0 {
		limit = 50
	}
	rows, err := e.query(`SELECT id, customer_name, amount_paise, amount_paid, due_at, status
        FROM invoices WHERE status IN ('issued','partially_paid','expired')
        AND amount_paid < amount_paise ORDER BY due_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var outstanding int64
	n, overdue := 0, 0
	now := time.Now()
	for rows.Next() {
		var id, name, st string
		var amt, paid, due int64
		if err := rows.Scan(&id, &name, &amt, &paid, &due, &st); err != nil {
			return nil, err
		}
		n++
		bal := amt - paid
		outstanding += bal
		late := ""
		if time.Unix(due, 0).Before(now) {
			overdue++
			late = " · overdue"
		}
		fs.Tables["invoices"] = append(fs.Tables["invoices"], Row{
			Label: name, Amount: bal,
			Extra: fmt.Sprintf("%s · due %s%s", id, time.Unix(due, 0).Format("2 Jan"), late)})
	}
	fs.add("unpaid_count", fmt.Sprintf("%d", n), float64(n))
	fs.add("outstanding_total", money.FormatShort(outstanding), float64(outstanding))
	fs.add("overdue_count", fmt.Sprintf("%d", overdue), float64(overdue))
	fs.Rows = n
	e.stamp(fs, Window{From: now, To: now})
	fs.WindowFrom, fs.WindowTo = "all", "open"
	return fs, nil
}

// SettlementSummary totals settlements in the window.
func (e *Engine) SettlementSummary(w Window) (*FactSet, error) {
	fs := &FactSet{Capability: "settlement_summary", Tables: map[string][]Row{}}
	rows, err := e.query(`SELECT status, COUNT(*), COALESCE(SUM(amount_paise),0),
        COALESCE(SUM(fees_paise),0), COALESCE(SUM(tax_paise),0)
        FROM settlements WHERE created_at >= ? AND created_at < ? GROUP BY status`,
		w.From.Unix(), w.To.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var total, fees, tax int64
	n := 0
	for rows.Next() {
		var st string
		var c int
		var amt, f, t int64
		if err := rows.Scan(&st, &c, &amt, &f, &t); err != nil {
			return nil, err
		}
		n += c
		if st == "processed" {
			total += amt
			fees += f
			tax += t
		}
		fs.Tables["by_status"] = append(fs.Tables["by_status"], Row{Label: st, Count: c, Amount: amt})
	}
	fs.add("settled_total", money.FormatShort(total), float64(total))
	fs.add("settlement_count", fmt.Sprintf("%d", n), float64(n))
	fs.add("fees_total", money.FormatShort(fees), float64(fees))
	fs.add("tax_total", money.FormatShort(tax), float64(tax))
	fs.Rows = n
	e.stamp(fs, w)
	return fs, nil
}

// DuplicateDetection finds same-amount, same-contact captured payments inside
// a short window of each other.
func (e *Engine) DuplicateDetection(w Window, within time.Duration) (*FactSet, error) {
	fs := &FactSet{Capability: "duplicate_detection", Tables: map[string][]Row{}}
	rows, err := e.query(`SELECT a.id, b.id, a.contact, a.amount_paise, a.created_at, b.created_at
        FROM payments a JOIN payments b
          ON a.contact = b.contact AND a.amount_paise = b.amount_paise
         AND a.id < b.id AND ABS(a.created_at - b.created_at) <= ?
        WHERE a.status='captured' AND b.status='captured' AND a.contact != ''
          AND a.created_at >= ? AND a.created_at < ?
        ORDER BY a.created_at DESC`, int64(within.Seconds()), w.From.Unix(), w.To.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var exposure int64
	n := 0
	for rows.Next() {
		var aID, bID, contact string
		var amt, at, bt int64
		if err := rows.Scan(&aID, &bID, &contact, &amt, &at, &bt); err != nil {
			return nil, err
		}
		n++
		exposure += amt
		gap := time.Duration(abs64(bt-at)) * time.Second
		fs.Tables["pairs"] = append(fs.Tables["pairs"], Row{
			Label: aID + " / " + bID, Amount: amt,
			Extra: fmt.Sprintf("%s · %s apart", RedactContact(contact), gap.Round(time.Minute))})
	}
	fs.add("duplicate_pairs", fmt.Sprintf("%d", n), float64(n))
	fs.add("duplicate_exposure", money.FormatShort(exposure), float64(exposure))
	fs.add("window_minutes", fmt.Sprintf("%d", int(within.Minutes())), within.Minutes())
	fs.Rows = n
	e.stamp(fs, w)
	return fs, nil
}

// EntityLookup reads one entity from the mirror. The write plane never uses
// this path; it calls FetchPayment for a fresh read instead.
func (e *Engine) EntityLookup(id string) (*FactSet, error) {
	fs := &FactSet{Capability: "entity_lookup", Tables: map[string][]Row{}}
	var st, method, email, contact, ec, er string
	var amt, refunded, ts int64
	err := e.row(`SELECT status, method, COALESCE(email,''), COALESCE(contact,''),
        COALESCE(error_code,''), COALESCE(error_reason,''), amount_paise, refunded_paise, created_at
        FROM payments WHERE id=?`, id).Scan(&st, &method, &email, &contact, &ec, &er, &amt, &refunded, &ts)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no entity %s in the local mirror; run `finterminal sync`", id)
	}
	if err != nil {
		return nil, err
	}
	fs.add("entity_id", id, 0)
	fs.add("amount", money.FormatShort(amt), float64(amt))
	fs.add("refunded", money.FormatShort(refunded), float64(refunded))
	fs.add("status", st, 0)
	fs.add("method", method, 0)
	fs.add("created", time.Unix(ts, 0).Format("2 Jan 2006, 15:04"), 0)
	if ec != "" {
		fs.Notes = append(fs.Notes, ec+" / "+er)
	}
	fs.Meta = map[string]string{"email": email, "contact": RedactContact(contact)}
	fs.Rows = 1
	e.stamp(fs, Window{From: time.Now(), To: time.Now()})
	fs.WindowFrom, fs.WindowTo = "n/a", "n/a"
	return fs, nil
}

// PaymentView is the shape the write plane renders its confirmation from.
type PaymentView struct {
	ID          string
	Status      string
	Method      string
	AmountPaise int64
	Refunded    int64
	Email       string
	Contact     string
	CreatedAt   time.Time
	FetchedAt   time.Time
}

// FetchPayment reads a payment directly, bypassing any FactSet. The write
// plane calls this immediately before rendering a confirmation.
func FetchPayment(s *store.Store, id string) (*PaymentView, error) {
	var v PaymentView
	var ts int64
	err := s.DB.QueryRow(`SELECT id, status, method, amount_paise, refunded_paise,
        COALESCE(email,''), COALESCE(contact,''), created_at FROM payments WHERE id=?`, id).
		Scan(&v.ID, &v.Status, &v.Method, &v.AmountPaise, &v.Refunded, &v.Email, &v.Contact, &ts)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("payment %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	v.CreatedAt = time.Unix(ts, 0)
	v.FetchedAt = time.Now()
	return &v, nil
}

// RedactContact keeps only the last four digits of a phone number.
func RedactContact(c string) string {
	if len(c) <= 4 {
		return "***"
	}
	return "*******" + c[len(c)-4:]
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func abs64(i int64) int64 {
	if i < 0 {
		return -i
	}
	return i
}

func sign(i int64) int {
	switch {
	case i > 0:
		return 1
	case i < 0:
		return -1
	}
	return 0
}

func direction(delta int64) string {
	switch {
	case delta > 0:
		return "rose"
	case delta < 0:
		return "fell"
	}
	return "held flat"
}

// SortRows sorts a table by count descending, stably, for deterministic output.
func SortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })
}
