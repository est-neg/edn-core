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

	// Deduplication: reject same whatsapp+profile within the configured window.
	window := time.Duration(s.cfg.DedupWindowSec) * time.Second
	if window == 0 {
		window = 5 * time.Minute
	}
	since := receivedAt.Add(-window)

	exists, err := s.repo.ExistsByWhatsAppAndProfile(ctx, req.Lead.WhatsApp, req.Lead.Profile, since)
	if err != nil {
		return fmt.Errorf("dedup check: %w", err)
	}
	if exists {
		return ErrDuplicateLead
	}

	submittedAt, err := time.Parse(time.RFC3339, req.SubmittedAt)
	if err != nil {
		return &ValidationError{Details: "submittedAt must be a valid RFC3339 timestamp"}
	}

	lead := Lead{
		ID:                 uuid.New().String(),
		DedupKey:           buildDedupKey(req.Lead.WhatsApp, req.Lead.Profile, receivedAt, window),
		Source:             req.Source,
		SubmittedAt:        submittedAt.UTC(),
		ReceivedAt:         receivedAt,
		Name:               req.Lead.Name,
		BusinessName:       req.Lead.BusinessName,
		WhatsApp:           req.Lead.WhatsApp,
		Email:              req.Lead.Email,
		Profile:            req.Lead.Profile,
		Message:            req.Lead.Message,
		Consent:            req.Lead.Consent,
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
		zap.String("profile", lead.Profile),
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

func buildDedupKey(whatsApp, profile string, receivedAt time.Time, window time.Duration) string {
	bucketSeconds := int64(window / time.Second)
	if bucketSeconds <= 0 {
		bucketSeconds = int64((5 * time.Minute) / time.Second)
	}
	bucket := receivedAt.Unix() / bucketSeconds
	return fmt.Sprintf("%s:%s:%d", whatsApp, profile, bucket)
}
