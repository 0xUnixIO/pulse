package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"pulse/internal/orders"
	"pulse/internal/store/postgres/sqlcgen"
)

type OrderStore struct {
	db *pgxpool.Pool
}

func (s *OrderStore) UpsertOrder(order orders.Order) (orders.Order, error) {
	if order.CreatedAt.IsZero() {
		order.CreatedAt = time.Now().UTC()
	}
	if order.Status == "" {
		order.Status = orders.StatusPending
	}
	if order.Currency == "" {
		order.Currency = "usd"
	}

	err := sqlcgen.New(s.db).UpsertOrder(context.Background(), sqlcgen.UpsertOrderParams{
		ID:                   order.ID,
		UserID:               order.UserID,
		PlanID:               order.PlanID,
		Email:                order.Email,
		StripeSessionID:      order.StripeSessionID,
		StripeSubscriptionID: order.StripeSubscriptionID,
		StripeCustomerID:     order.StripeCustomerID,
		Status:               order.Status,
		AmountCents:          int32(order.AmountCents),
		Currency:             order.Currency,
		CreatedAt:            order.CreatedAt.Format(time.RFC3339Nano),
		PaidAt:               formatTimePtr(order.PaidAt),
		LastInvoiceID:        order.LastInvoiceID,
	})
	if err != nil {
		return orders.Order{}, fmt.Errorf("upsert order: %w", err)
	}
	return order, nil
}

func (s *OrderStore) GetOrder(id string) (orders.Order, error) {
	row, err := sqlcgen.New(s.db).GetOrderByID(context.Background(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.Order{}, orders.ErrOrderNotFound
	}
	if err != nil {
		return orders.Order{}, err
	}
	return toOrder(row)
}

func (s *OrderStore) GetOrderByStripeSession(sessionID string) (orders.Order, error) {
	row, err := sqlcgen.New(s.db).GetOrderByStripeSession(context.Background(), sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.Order{}, orders.ErrOrderNotFound
	}
	if err != nil {
		return orders.Order{}, err
	}
	return toOrder(row)
}

func (s *OrderStore) GetOrderByStripeSubscription(subscriptionID string) (orders.Order, error) {
	row, err := sqlcgen.New(s.db).GetOrderByStripeSubscription(context.Background(), subscriptionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.Order{}, orders.ErrOrderNotFound
	}
	if err != nil {
		return orders.Order{}, err
	}
	return toOrder(row)
}

func (s *OrderStore) ListOrders() ([]orders.Order, error) {
	rows, err := sqlcgen.New(s.db).ListOrders(context.Background())
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return toOrders(rows)
}

func (s *OrderStore) ListOrdersByUser(userID string) ([]orders.Order, error) {
	rows, err := sqlcgen.New(s.db).ListOrdersByUser(context.Background(), userID)
	if err != nil {
		return nil, fmt.Errorf("list orders by user: %w", err)
	}
	return toOrders(rows)
}

func (s *OrderStore) ListOrdersByEmail(email string) ([]orders.Order, error) {
	rows, err := sqlcgen.New(s.db).ListOrdersByEmail(context.Background(), email)
	if err != nil {
		return nil, fmt.Errorf("list orders by email: %w", err)
	}
	return toOrders(rows)
}

func (s *OrderStore) ClaimInvoice(orderID, invoiceID string) (bool, error) {
	result, err := sqlcgen.New(s.db).ClaimInvoice(context.Background(), sqlcgen.ClaimInvoiceParams{
		LastInvoiceID: invoiceID,
		ID:            orderID,
	})
	if err != nil {
		return false, fmt.Errorf("claim invoice: %w", err)
	}
	return result.RowsAffected() > 0, nil
}

func (s *OrderStore) UnclaimInvoice(orderID, invoiceID string) error {
	_, err := sqlcgen.New(s.db).UnclaimInvoice(context.Background(), sqlcgen.UnclaimInvoiceParams{
		ID:            orderID,
		LastInvoiceID: invoiceID,
	})
	if err != nil {
		return fmt.Errorf("unclaim invoice: %w", err)
	}
	return nil
}

func (s *OrderStore) ClaimOrderPaid(orderID string, paidAt time.Time) (bool, error) {
	paidStr := paidAt.UTC().Format(time.RFC3339Nano)
	result, err := sqlcgen.New(s.db).ClaimOrderPaid(context.Background(), sqlcgen.ClaimOrderPaidParams{
		PaidAt: &paidStr,
		ID:     orderID,
	})
	if err != nil {
		return false, fmt.Errorf("claim order paid: %w", err)
	}
	return result.RowsAffected() > 0, nil
}

func (s *OrderStore) RevertOrderToPending(orderID string) error {
	_, err := sqlcgen.New(s.db).RevertOrderToPending(context.Background(), orderID)
	if err != nil {
		return fmt.Errorf("revert order to pending: %w", err)
	}
	return nil
}

func (s *OrderStore) DeleteOrder(id string) error {
	result, err := sqlcgen.New(s.db).DeleteOrderByID(context.Background(), id)
	if err != nil {
		return fmt.Errorf("delete order: %w", err)
	}
	if result.RowsAffected() == 0 {
		return orders.ErrOrderNotFound
	}
	return nil
}

// ─── 类型转换辅助 ─────────────────────────────────────────────────────────────

func toOrder(r sqlcgen.Order) (orders.Order, error) {
	o := orders.Order{
		ID:                   r.ID,
		UserID:               r.UserID,
		PlanID:               r.PlanID,
		Email:                r.Email,
		StripeSessionID:      r.StripeSessionID,
		StripeSubscriptionID: r.StripeSubscriptionID,
		StripeCustomerID:     r.StripeCustomerID,
		Status:               r.Status,
		AmountCents:          int(r.AmountCents),
		Currency:             r.Currency,
		LastInvoiceID:        r.LastInvoiceID,
	}
	if r.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
		if err != nil {
			return orders.Order{}, fmt.Errorf("parse order created_at: %w", err)
		}
		o.CreatedAt = t
	}
	if r.PaidAt != nil && *r.PaidAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *r.PaidAt)
		if err != nil {
			return orders.Order{}, fmt.Errorf("parse order paid_at: %w", err)
		}
		o.PaidAt = &t
	}
	return o, nil
}

func toOrders(rows []sqlcgen.Order) ([]orders.Order, error) {
	out := make([]orders.Order, 0, len(rows))
	for _, r := range rows {
		o, err := toOrder(r)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}
