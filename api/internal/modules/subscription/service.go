package subscription

import (
	"context"
	"errors"

	"fmt"
	"github.com/jackc/pgx/v5"
	"time"

	"github.com/nyxsr/meme-portal/api/internal/platform/token"
)

// PaymentGateway abstraksi payment provider; saat ini mock.
type PaymentGateway interface {
	CreateCharge(ctx context.Context, amount int64, reference string) (checkoutURL string, err error)
}

// MockGateway mensimulasikan payment gateway: charge langsung "berhasil dibuat"
// dan konfirmasi dilakukan lewat endpoint confirm milik API sendiri.
type MockGateway struct{}

func (MockGateway) CreateCharge(_ context.Context, _ int64, reference string) (string, error) {
	return "/api/v1/subscription/payments/" + reference + "/confirm", nil
}

type Service struct {
	repo    *Repository
	gateway PaymentGateway
}

func NewService(repo *Repository, gateway PaymentGateway) *Service {
	return &Service{repo: repo, gateway: gateway}
}

func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) {
	return s.repo.ListPlans(ctx)
}

// Checkout membuat checkout baru; jika idempotencyKey pernah dipakai,
// checkout yang sama dikembalikan (replay) tanpa membuat baris baru.
func (s *Service) Checkout(ctx context.Context, userID string, dto CheckoutDTO, idempotencyKey string) (CheckoutResponse, error) {
	if prev, ok, err := s.repo.CheckoutByIdempotencyKey(ctx, idempotencyKey); err != nil {
		return CheckoutResponse{}, err
	} else if ok {
		return prev, nil
	}

	plan, err := s.repo.GetPlanByCode(ctx, dto.PlanCode)
	if err != nil {
		return CheckoutResponse{}, err
	}
	if _, _, err := s.repo.ActiveByUser(ctx, userID); err == nil {
		return CheckoutResponse{}, ErrAlreadySubscribed
	}

	reference := token.RandomHex(12)
	sub, pay, err := s.repo.CreateSubscription(ctx, userID, plan, reference, idempotencyKey)
	if err != nil {
		return CheckoutResponse{}, err
	}
	url, err := s.gateway.CreateCharge(ctx, pay.AmountIDR, reference)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("subscription service: create charge gagal: %w", err)
	}
	return CheckoutResponse{Subscription: sub, Payment: pay, MockGatewayURL: url}, nil
}

// Confirm mensimulasikan callback payment gateway sukses.
func (s *Service) Confirm(ctx context.Context, reference string) (Subscription, Payment, error) {
	return s.repo.ConfirmPayment(ctx, reference)
}

// MarkExpired menandai langganan aktif yang sudah lewat masa aktif
// (dipanggil janitor periodik).
func (s *Service) MarkExpired(ctx context.Context) (int64, error) {
	return s.repo.MarkExpired(ctx)
}

// CheckQuota locks the user until the caller commits the pending meme.
func (s *Service) CheckQuota(ctx context.Context, tx pgx.Tx, userID string) error {
	var lockedID string
	if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE", userID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotSubscribed
		}
		return err
	}
	sub, plan, err := activeByUser(ctx, tx, userID)
	if err != nil {
		return err
	}
	if plan.MaxMemes == 0 {
		return nil
	}
	if sub.StartsAt == nil {
		return ErrNotSubscribed
	}
	used, err := memesUsed(ctx, tx, userID, *sub.StartsAt)
	if err != nil {
		return err
	}
	if used >= plan.MaxMemes {
		return ErrQuotaExceeded
	}
	return nil
}

func (s *Service) Status(ctx context.Context, userID string) (SubscriptionStatus, error) {
	sub, plan, err := s.repo.ActiveByUser(ctx, userID)
	if err == nil {
		if sub.StartsAt == nil {
			return SubscriptionStatus{}, fmt.Errorf("langganan aktif tidak punya tanggal mulai")
		}
		used, err := s.repo.MemesUsed(ctx, userID, *sub.StartsAt)
		if err != nil {
			return SubscriptionStatus{}, err
		}
		var endsAt *string
		if sub.EndsAt != nil {
			s := sub.EndsAt.UTC().Format(time.RFC3339)
			endsAt = &s
		}
		return SubscriptionStatus{
			Subscribed: true,
			Status:     sub.Status,
			PlanCode:   plan.Code,
			PlanName:   plan.Name,
			EndsAt:     endsAt,
			MemesUsed:  used,
			MaxMemes:   plan.MaxMemes,
		}, nil
	}
	if !errors.Is(err, ErrNotSubscribed) {
		return SubscriptionStatus{}, err
	}
	return SubscriptionStatus{Subscribed: false, MemesUsed: 0, MaxMemes: 0}, nil
}
