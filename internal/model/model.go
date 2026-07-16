package model

import "time"

type Product struct {
	ID              string     `json:"id"`
	Code            string     `json:"code"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	NotifyURL       string     `json:"notifyUrl"`
	ReturnURL       string     `json:"returnUrl,omitempty"`
	APISecretEnc    string     `json:"-"`
	NotifySecretEnc string     `json:"-"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	DisabledAt      *time.Time `json:"disabledAt,omitempty"`
}

type Order struct {
	ID                string     `json:"id"`
	ProductID         string     `json:"productId"`
	ProductName       string     `json:"productName,omitempty"`
	ProductOrderNo    string     `json:"productOrderNo"`
	PayID             string     `json:"payId"`
	EpayOrderID       string     `json:"epayOrderId,omitempty"`
	PayType           int        `json:"payType"`
	GoodsName         string     `json:"goodsName"`
	AmountCents       int64      `json:"amountCents"`
	ReallyAmountCents *int64     `json:"reallyAmountCents,omitempty"`
	Status            string     `json:"status"`
	PayURL            string     `json:"payUrl,omitempty"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	PaidAt            *time.Time `json:"paidAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	DeliveryID        string     `json:"deliveryId,omitempty"`
	DeliveryStatus    string     `json:"deliveryStatus,omitempty"`
	DeliveryAttempts  int        `json:"deliveryAttempts,omitempty"`
	DeliveryLastError string     `json:"deliveryLastError,omitempty"`
}

type Delivery struct {
	ID              string
	OrderID         string
	ProductID       string
	Endpoint        string
	EventType       string
	Payload         string
	Status          string
	AttemptCount    int
	NextAttemptAt   time.Time
	LockedUntil     *time.Time
	HTTPStatus      *int
	LastError       string
	DeliveredAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	NotifySecretEnc string
}

type Stats struct {
	Products       int64 `json:"products"`
	ActiveProducts int64 `json:"activeProducts"`
	Orders         int64 `json:"orders"`
	PaidOrders     int64 `json:"paidOrders"`
	PendingOrders  int64 `json:"pendingOrders"`
	FailedDelivery int64 `json:"failedDelivery"`
	PaidCents      int64 `json:"paidCents"`
}
