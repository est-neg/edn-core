package leads

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// Submitter is the use-case interface consumed by the HTTP handler.
type Submitter interface {
	Submit(ctx context.Context, req SubmitRequest) error
}

// Service implements Submitter and orchestrates lead submission.
type Service struct {
	repo     Repository
	notifier Notifier
	cfg      config.LeadsConfig
	log      *zap.Logger
	enabled  bool
}

// NewService constructs a Service with its required dependencies.
func NewService(repo Repository, notifier Notifier, cfg config.LeadsConfig, log *zap.Logger) *Service {
	return &Service{
		repo:     repo,
		notifier: notifier,
		cfg:      cfg,
		log:      log,
		enabled:  cfg.NotificationWebhookURL != "",
	}
}

// Submit validates, deduplicates, persists and triggers notification for a lead.
func (s *Service) Submit(ctx context.Context, req SubmitRequest) error {
	receivedAt := time.Now().UTC()

	// Deduplication: email is permanently unique across all leads.
	exists, err := s.repo.ExistsByEmail(ctx, req.Email)
	if err != nil {
		return fmt.Errorf("dedup check: %w", err)
	}
	if exists {
		return ErrDuplicateLead
	}

	// Phone uniqueness: reject if another lead already has this number.
	if req.Phone != "" {
		phoneExists, err := s.repo.ExistsByPhone(ctx, req.Phone)
		if err != nil {
			return fmt.Errorf("phone dedup check: %w", err)
		}
		if phoneExists {
			return ErrDuplicateLead
		}
	}

	lead := Lead{
		ID:                 uuid.New().String(),
		DedupKey:           req.Email,
		TenantID:           s.cfg.TenantID,
		Source:             req.Source,
		ReceivedAt:         receivedAt,
		Name:               req.Name,
		Email:              req.Email,
		Phone:              req.Phone,
		Status:             "new",
		NotificationStatus: s.initialNotificationStatus(),
	}

	if err := s.repo.Save(ctx, lead); err != nil {
		if errors.Is(err, ErrDuplicateLead) {
			return ErrDuplicateLead
		}
		return fmt.Errorf("save lead: %w", err)
	}

	s.log.Info("lead submitted",
		zap.String("lead_id", lead.ID),
		zap.String("source", lead.Source),
		zap.String("email", lead.Email),
	)

	if !s.enabled {
		return nil
	}

	// Notification is a best-effort side effect; the HTTP response is
	// unblocked once the lead is persisted. The persistence state is updated
	// so operations can see whether delivery succeeded.
	go s.notifyLead(lead)

	return nil
}

func (s *Service) initialNotificationStatus() string {
	if s.enabled {
		return "pending"
	}
	return "disabled"
}

func (s *Service) notifyLead(lead Lead) {
	timeout := time.Duration(s.cfg.NotificationTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	notifyCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := s.notifier.Notify(notifyCtx, lead); err != nil {
		if updateErr := s.repo.UpdateNotificationStatus(context.Background(), lead.ID, "failed", err.Error()); updateErr != nil {
			s.log.Warn("lead notification status update failed",
				zap.String("lead_id", lead.ID),
				zap.Error(updateErr),
			)
		}
		s.log.Warn("lead notification failed",
			zap.String("lead_id", lead.ID),
			zap.Error(err),
		)
		return
	}

	if err := s.repo.UpdateNotificationStatus(context.Background(), lead.ID, "sent", ""); err != nil {
		s.log.Warn("lead notification status update failed",
			zap.String("lead_id", lead.ID),
			zap.Error(err),
		)
	}
}
