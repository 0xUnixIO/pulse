package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"pulse/internal/plans"
	"pulse/internal/store/postgres/sqlcgen"
)

type PlanStore struct {
	db *pgxpool.Pool
}

func (s *PlanStore) UpsertPlan(plan plans.Plan) (plans.Plan, error) {
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = time.Now().UTC()
	}
	if plan.Currency == "" {
		plan.Currency = "usd"
	}
	if plan.Mode == "" {
		plan.Mode = "live"
	}
	if plan.StockLimit == 0 && plan.StockSold == 0 {
		plan.StockLimit = -1
	}

	err := sqlcgen.New(s.db).UpsertPlan(context.Background(), sqlcgen.UpsertPlanParams{
		ID:                     plan.ID,
		Name:                   plan.Name,
		Description:            plan.Description,
		Type:                   plan.Type,
		PriceCents:             int32(plan.PriceCents),
		Currency:               plan.Currency,
		StripePriceID:          plan.StripePriceID,
		TrafficLimit:           plan.TrafficLimit,
		DurationDays:           int32(plan.DurationDays),
		DataLimitResetStrategy: plan.DataLimitResetStrategy,
		UserGroupIds:           plan.UserGroupIDs,
		SortOrder:              int32(plan.SortOrder),
		Enabled:                int32(boolToInt(plan.Enabled)),
		Mode:                   plan.Mode,
		StockLimit:             int32(plan.StockLimit),
		StockSold:              int32(plan.StockSold),
		CreatedAt:              plan.CreatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return plans.Plan{}, fmt.Errorf("upsert plan: %w", err)
	}
	return plan, nil
}

func (s *PlanStore) GetPlan(id string) (plans.Plan, error) {
	row, err := sqlcgen.New(s.db).GetPlanByID(context.Background(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return plans.Plan{}, plans.ErrPlanNotFound
	}
	if err != nil {
		return plans.Plan{}, err
	}
	return toPlan(row)
}

func (s *PlanStore) ListPlans() ([]plans.Plan, error) {
	rows, err := sqlcgen.New(s.db).ListPlans(context.Background())
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	return toPlans(rows)
}

func (s *PlanStore) ListEnabledPlans() ([]plans.Plan, error) {
	rows, err := sqlcgen.New(s.db).ListEnabledPlans(context.Background())
	if err != nil {
		return nil, fmt.Errorf("list enabled plans: %w", err)
	}
	return toPlans(rows)
}

func (s *PlanStore) ListEnabledPlansByMode(mode string) ([]plans.Plan, error) {
	rows, err := sqlcgen.New(s.db).ListEnabledPlansByMode(context.Background(), mode)
	if err != nil {
		return nil, fmt.Errorf("list enabled plans by mode: %w", err)
	}
	return toPlans(rows)
}

func (s *PlanStore) IncrementStockSold(planID string, quantity int) (bool, error) {
	result, err := sqlcgen.New(s.db).IncrementPlanStockSold(context.Background(), sqlcgen.IncrementPlanStockSoldParams{
		Quantity: int32(quantity),
		PlanID:   planID,
	})
	if err != nil {
		return false, fmt.Errorf("increment stock sold: %w", err)
	}
	return result.RowsAffected() > 0, nil
}

func (s *PlanStore) DeletePlan(id string) error {
	result, err := sqlcgen.New(s.db).DeletePlanByID(context.Background(), id)
	if err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}
	if result.RowsAffected() == 0 {
		return plans.ErrPlanNotFound
	}
	return nil
}

// ─── 类型转换辅助 ─────────────────────────────────────────────────────────────

func toPlan(r sqlcgen.Plan) (plans.Plan, error) {
	p := plans.Plan{
		ID:                     r.ID,
		Name:                   r.Name,
		Description:            r.Description,
		Type:                   r.Type,
		PriceCents:             int(r.PriceCents),
		Currency:               r.Currency,
		StripePriceID:          r.StripePriceID,
		TrafficLimit:           r.TrafficLimit,
		DurationDays:           int(r.DurationDays),
		DataLimitResetStrategy: r.DataLimitResetStrategy,
		UserGroupIDs:           r.UserGroupIds,
		SortOrder:              int(r.SortOrder),
		Enabled:                r.Enabled != 0,
		Mode:                   r.Mode,
		StockLimit:             int(r.StockLimit),
		StockSold:              int(r.StockSold),
	}
	if r.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
		if err != nil {
			return plans.Plan{}, fmt.Errorf("parse plan created_at: %w", err)
		}
		p.CreatedAt = t
	}
	return p, nil
}

func toPlans(rows []sqlcgen.Plan) ([]plans.Plan, error) {
	out := make([]plans.Plan, 0, len(rows))
	for _, r := range rows {
		p, err := toPlan(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
