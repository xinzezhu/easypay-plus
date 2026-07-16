package store

import (
	"context"
	"errors"
	"time"

	"github.com/easypay-plus/easypay-plus/internal/model"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrReplay   = errors.New("request replayed")
	ErrMismatch = errors.New("payment data mismatch")
)

type PaymentConfirmation struct {
	OrderID           string
	PayID             string
	EpayOrderID       string
	PayType           int
	AmountCents       int64
	ReallyAmountCents int64
	RawNotify         string
	PaidAt            time.Time
	Delivery          model.Delivery
}

type Store interface {
	Close() error
	Health(context.Context) error
	CreateProduct(context.Context, model.Product) error
	ListProducts(context.Context) ([]model.Product, error)
	GetProduct(context.Context, string) (model.Product, error)
	SetProductStatus(context.Context, string, string, time.Time) error
	InsertNonce(context.Context, string, string, time.Time) error
	CreateOrder(context.Context, model.Order) error
	GetOrder(context.Context, string) (model.Order, error)
	GetOrderByProductNo(context.Context, string, string) (model.Order, error)
	FinalizeOrder(context.Context, model.Order) error
	FailOrder(context.Context, string) error
	ExpireDueOrders(context.Context, time.Time) (int64, error)
	ConfirmPayment(context.Context, PaymentConfirmation) (bool, error)
	ListOrders(context.Context, string, string, int) ([]model.Order, error)
	Stats(context.Context) (model.Stats, error)
	ClaimDelivery(context.Context, time.Time) (*model.Delivery, error)
	CompleteDelivery(context.Context, string, bool, bool, time.Time, *int, string) error
	RetryDelivery(context.Context, string, time.Time) error
}
