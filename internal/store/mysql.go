package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/easypay-plus/easypay-plus/internal/model"
	"github.com/easypay-plus/easypay-plus/migrations"
	"github.com/go-sql-driver/mysql"
)

type MySQL struct {
	db *sql.DB
}

func OpenMySQL(dsn string, autoMigrate bool) (*MySQL, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect mysql: %w", err)
	}
	if autoMigrate {
		if _, err := db.ExecContext(ctx, migrations.InitialSchema); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply schema: %w", err)
		}
	}
	return &MySQL{db: db}, nil
}

func (s *MySQL) Close() error { return s.db.Close() }

func (s *MySQL) Health(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *MySQL) CreateProduct(ctx context.Context, p model.Product) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO products (id, code, name, status, notify_url, return_url, api_secret_enc, notify_secret_enc, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Code, p.Name, p.Status, p.NotifyURL, p.ReturnURL, p.APISecretEnc, p.NotifySecretEnc, p.CreatedAt, p.UpdatedAt)
	return mapWriteError(err)
}

func (s *MySQL) ListProducts(ctx context.Context) ([]model.Product, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code, name, status, notify_url, return_url, api_secret_enc, notify_secret_enc, created_at, updated_at, disabled_at
		FROM products ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	products := make([]model.Product, 0)
	for rows.Next() {
		product, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	return products, rows.Err()
}

func (s *MySQL) GetProduct(ctx context.Context, id string) (model.Product, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, code, name, status, notify_url, return_url, api_secret_enc, notify_secret_enc, created_at, updated_at, disabled_at
		FROM products WHERE id = ?`, id)
	product, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Product{}, ErrNotFound
	}
	return product, err
}

func (s *MySQL) SetProductStatus(ctx context.Context, id, status string, now time.Time) error {
	var disabledAt any
	if status == "disabled" {
		disabledAt = now
	}
	result, err := s.db.ExecContext(ctx, `UPDATE products SET status = ?, disabled_at = ?, updated_at = ? WHERE id = ?`, status, disabledAt, now, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *MySQL) InsertNonce(ctx context.Context, productID, nonce string, expiresAt time.Time) error {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM api_nonces WHERE expires_at < ? LIMIT 500`, time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_nonces (product_id, nonce, expires_at) VALUES (?, ?, ?)`, productID, nonce, expiresAt)
	if isDuplicate(err) {
		return ErrReplay
	}
	return err
}

func (s *MySQL) CreateOrder(ctx context.Context, o model.Order) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO payment_orders
		(id, product_id, product_order_no, pay_id, pay_type, goods_name, amount_cents, status, pay_url, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.ProductID, o.ProductOrderNo, o.PayID, o.PayType, o.GoodsName, o.AmountCents, o.Status, o.PayURL, o.CreatedAt, o.UpdatedAt)
	return mapWriteError(err)
}

const orderSelect = `
	SELECT o.id, o.product_id, p.name, o.product_order_no, o.pay_id, COALESCE(o.epay_order_id, ''),
		o.pay_type, o.goods_name, o.amount_cents, o.really_amount_cents, o.status, o.pay_url,
		o.expires_at, o.paid_at, o.created_at, o.updated_at,
		COALESCE(d.id, ''), COALESCE(d.status, ''), COALESCE(d.attempt_count, 0), COALESCE(d.last_error, '')
	FROM payment_orders o
	JOIN products p ON p.id = o.product_id
	LEFT JOIN deliveries d ON d.order_id = o.id AND d.event_type = 'payment.succeeded'`

func (s *MySQL) GetOrder(ctx context.Context, id string) (model.Order, error) {
	order, err := scanOrder(s.db.QueryRowContext(ctx, orderSelect+` WHERE o.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Order{}, ErrNotFound
	}
	return order, err
}

func (s *MySQL) GetOrderByProductNo(ctx context.Context, productID, productOrderNo string) (model.Order, error) {
	order, err := scanOrder(s.db.QueryRowContext(ctx, orderSelect+` WHERE o.product_id = ? AND o.product_order_no = ?`, productID, productOrderNo))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Order{}, ErrNotFound
	}
	return order, err
}

func (s *MySQL) FinalizeOrder(ctx context.Context, o model.Order) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE payment_orders SET epay_order_id = ?, really_amount_cents = ?, status = ?, pay_url = ?, expires_at = ?, updated_at = ?
		WHERE id = ? AND status = 'creating'`,
		nullIfEmpty(o.EpayOrderID), o.ReallyAmountCents, o.Status, o.PayURL, o.ExpiresAt, o.UpdatedAt, o.ID)
	if err != nil {
		return mapWriteError(err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrConflict
	}
	return nil
}

func (s *MySQL) FailOrder(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE payment_orders SET status = 'failed', updated_at = ? WHERE id = ? AND status = 'creating'`, time.Now(), id)
	return err
}

func (s *MySQL) ConfirmPayment(ctx context.Context, confirmation PaymentConfirmation) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var payID, epayOrderID, status, productID string
	var payType int
	var amount int64
	err = tx.QueryRowContext(ctx, `
		SELECT pay_id, COALESCE(epay_order_id, ''), pay_type, amount_cents, status, product_id
		FROM payment_orders WHERE id = ? FOR UPDATE`, confirmation.OrderID).
		Scan(&payID, &epayOrderID, &payType, &amount, &status, &productID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if (confirmation.PayID != "" && confirmation.PayID != payID) ||
		(confirmation.EpayOrderID != "" && epayOrderID != "" && confirmation.EpayOrderID != epayOrderID) ||
		confirmation.PayType != payType || confirmation.AmountCents != amount || productID != confirmation.Delivery.ProductID {
		return false, ErrMismatch
	}
	created := false
	if status != "paid" {
		if status != "pending" {
			return false, ErrConflict
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE payment_orders SET status = 'paid', really_amount_cents = ?, notify_raw = ?, paid_at = ?, updated_at = ? WHERE id = ?`,
			confirmation.ReallyAmountCents, confirmation.RawNotify, confirmation.PaidAt, confirmation.PaidAt, confirmation.OrderID)
		if err != nil {
			return false, err
		}
		created = true
	}
	d := confirmation.Delivery
	_, err = tx.ExecContext(ctx, `
		INSERT INTO deliveries
		(id, order_id, product_id, event_type, endpoint, payload, status, attempt_count, next_attempt_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, '', ?, ?)
		ON DUPLICATE KEY UPDATE id = id`,
		d.ID, d.OrderID, d.ProductID, d.EventType, d.Endpoint, d.Payload, d.NextAttemptAt, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

func (s *MySQL) ListOrders(ctx context.Context, status, productID string, limit int) ([]model.Order, error) {
	query := orderSelect + ` WHERE 1 = 1`
	args := make([]any, 0, 3)
	if status != "" {
		query += ` AND o.status = ?`
		args = append(args, status)
	}
	if productID != "" {
		query += ` AND o.product_id = ?`
		args = append(args, productID)
	}
	query += ` ORDER BY o.created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := make([]model.Order, 0)
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (s *MySQL) Stats(ctx context.Context) (model.Stats, error) {
	var stats model.Stats
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM products),
			(SELECT COUNT(*) FROM products WHERE status = 'active'),
			(SELECT COUNT(*) FROM payment_orders),
			(SELECT COUNT(*) FROM payment_orders WHERE status = 'paid'),
			(SELECT COUNT(*) FROM payment_orders WHERE status IN ('creating', 'pending')),
			(SELECT COUNT(*) FROM deliveries WHERE status = 'failed'),
			(SELECT COALESCE(SUM(really_amount_cents), 0) FROM payment_orders WHERE status = 'paid')`).
		Scan(&stats.Products, &stats.ActiveProducts, &stats.Orders, &stats.PaidOrders, &stats.PendingOrders, &stats.FailedDelivery, &stats.PaidCents)
	return stats, err
}

func (s *MySQL) ClaimDelivery(ctx context.Context, now time.Time) (*model.Delivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		SELECT d.id, d.order_id, d.product_id, d.endpoint, d.event_type, d.payload, d.status,
			d.attempt_count, d.next_attempt_at, d.locked_until, d.http_status, d.last_error,
			d.delivered_at, d.created_at, d.updated_at, p.notify_secret_enc
		FROM deliveries d JOIN products p ON p.id = d.product_id
		WHERE ((d.status IN ('pending', 'retrying') AND d.next_attempt_at <= ?)
			OR (d.status = 'processing' AND d.locked_until <= ?))
		ORDER BY d.next_attempt_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED`, now, now)
	delivery, err := scanDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lockedUntil := now.Add(time.Minute)
	_, err = tx.ExecContext(ctx, `UPDATE deliveries SET status = 'processing', locked_until = ?, updated_at = ? WHERE id = ?`, lockedUntil, now, delivery.ID)
	if err != nil {
		return nil, err
	}
	delivery.Status = "processing"
	delivery.LockedUntil = &lockedUntil
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &delivery, nil
}

func (s *MySQL) CompleteDelivery(ctx context.Context, id string, success, terminal bool, next time.Time, httpStatus *int, lastError string) error {
	status := "retrying"
	var deliveredAt any
	if success {
		status = "delivered"
		deliveredAt = time.Now()
	} else if terminal {
		status = "failed"
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries SET status = ?, attempt_count = attempt_count + 1, next_attempt_at = ?, locked_until = NULL,
			http_status = ?, last_error = ?, delivered_at = ?, updated_at = ? WHERE id = ?`,
		status, next, httpStatus, lastError, deliveredAt, time.Now(), id)
	return err
}

func (s *MySQL) RetryDelivery(ctx context.Context, id string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE deliveries SET status = 'retrying', attempt_count = 0, next_attempt_at = ?, locked_until = NULL,
			last_error = '', updated_at = ? WHERE id = ?`, now, now, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanProduct(row scanner) (model.Product, error) {
	var p model.Product
	var disabled sql.NullTime
	err := row.Scan(&p.ID, &p.Code, &p.Name, &p.Status, &p.NotifyURL, &p.ReturnURL, &p.APISecretEnc, &p.NotifySecretEnc, &p.CreatedAt, &p.UpdatedAt, &disabled)
	if disabled.Valid {
		p.DisabledAt = &disabled.Time
	}
	return p, err
}

func scanOrder(row scanner) (model.Order, error) {
	var o model.Order
	var really sql.NullInt64
	var expires, paid sql.NullTime
	err := row.Scan(&o.ID, &o.ProductID, &o.ProductName, &o.ProductOrderNo, &o.PayID, &o.EpayOrderID,
		&o.PayType, &o.GoodsName, &o.AmountCents, &really, &o.Status, &o.PayURL,
		&expires, &paid, &o.CreatedAt, &o.UpdatedAt,
		&o.DeliveryID, &o.DeliveryStatus, &o.DeliveryAttempts, &o.DeliveryLastError)
	if really.Valid {
		o.ReallyAmountCents = &really.Int64
	}
	if expires.Valid {
		o.ExpiresAt = &expires.Time
	}
	if paid.Valid {
		o.PaidAt = &paid.Time
	}
	return o, err
}

func scanDelivery(row scanner) (model.Delivery, error) {
	var d model.Delivery
	var locked, delivered sql.NullTime
	var httpStatus sql.NullInt64
	err := row.Scan(&d.ID, &d.OrderID, &d.ProductID, &d.Endpoint, &d.EventType, &d.Payload, &d.Status,
		&d.AttemptCount, &d.NextAttemptAt, &locked, &httpStatus, &d.LastError,
		&delivered, &d.CreatedAt, &d.UpdatedAt, &d.NotifySecretEnc)
	if locked.Valid {
		d.LockedUntil = &locked.Time
	}
	if delivered.Valid {
		d.DeliveredAt = &delivered.Time
	}
	if httpStatus.Valid {
		value := int(httpStatus.Int64)
		d.HTTPStatus = &value
	}
	return d, err
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapWriteError(err error) error {
	if isDuplicate(err) {
		return ErrConflict
	}
	return err
}

func isDuplicate(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
