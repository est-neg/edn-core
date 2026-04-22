package leads_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/leads"
	"github.com/villenneve/vil-core/internal/platform/config"
)

// --- fakes ---

type fakeRepo struct {
	saved     []leads.Lead
	saveErr   error
	exists    bool
	existsErr error
	updates   []notificationUpdate
}

type notificationUpdate struct {
	leadID string
	status string
	detail string
}

func (r *fakeRepo) Save(_ context.Context, lead leads.Lead) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, lead)
	return nil
}

func (r *fakeRepo) ExistsByWhatsAppAndProfile(_ context.Context, _, _ string, _ time.Time) (bool, error) {
	return r.exists, r.existsErr
}

func (r *fakeRepo) UpdateNotificationStatus(_ context.Context, leadID, status, detail string) error {
	r.updates = append(r.updates, notificationUpdate{leadID: leadID, status: status, detail: detail})
	return nil
}

func newService(t *testing.T, repo leads.Repository) *leads.Service {
	t.Helper()
	cfg := config.LeadsConfig{
		DedupWindowSec: 300,
	}
	return leads.NewService(repo, leads.NoopNotifier{}, cfg, zap.NewNop())
}

func validRequest() leads.SubmitRequest {
	return leads.SubmitRequest{
		Source:      "lumina-ia-site",
		SubmittedAt: time.Now().UTC().Format(time.RFC3339),
		Lead: leads.LeadPayload{
			Name:         "Maria Lima",
			BusinessName: "Studio Dental",
			WhatsApp:     "11988887777",
			Profile:      "dental",
			Consent:      true,
		},
	}
}

// --- tests ---

func TestService_Submit_Success(t *testing.T) {
	repo := &fakeRepo{}
	svc := newService(t, repo)

	if err := svc.Submit(context.Background(), validRequest()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("expected 1 saved lead, got %d", len(repo.saved))
	}

	saved := repo.saved[0]
	if saved.ID == "" {
		t.Error("expected lead.ID to be set")
	}
	if saved.Status != "new" {
		t.Errorf("expected status=new, got %q", saved.Status)
	}
	if saved.NotificationStatus != "disabled" {
		t.Errorf("expected notification_status=disabled, got %q", saved.NotificationStatus)
	}
	if saved.ReceivedAt.IsZero() {
		t.Error("expected ReceivedAt to be set")
	}
	if saved.DedupKey == "" {
		t.Error("expected DedupKey to be set")
	}
}

func TestService_Submit_DuplicateReturnsError(t *testing.T) {
	repo := &fakeRepo{exists: true}
	svc := newService(t, repo)

	err := svc.Submit(context.Background(), validRequest())
	if !errors.Is(err, leads.ErrDuplicateLead) {
		t.Errorf("expected ErrDuplicateLead, got %v", err)
	}
}

func TestService_Submit_PersistenceErrorBubbles(t *testing.T) {
	repo := &fakeRepo{saveErr: errors.New("insert failed")}
	svc := newService(t, repo)

	err := svc.Submit(context.Background(), validRequest())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestService_Submit_DedupCheckErrorBubbles(t *testing.T) {
	repo := &fakeRepo{existsErr: errors.New("db timeout")}
	svc := newService(t, repo)

	err := svc.Submit(context.Background(), validRequest())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestService_Submit_ReceivedAtIsServerTime(t *testing.T) {
	repo := &fakeRepo{}
	svc := newService(t, repo)
	before := time.Now().UTC()

	if err := svc.Submit(context.Background(), validRequest()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := time.Now().UTC()

	saved := repo.saved[0]
	if saved.ReceivedAt.Before(before) || saved.ReceivedAt.After(after) {
		t.Errorf("ReceivedAt %v outside expected range [%v, %v]", saved.ReceivedAt, before, after)
	}
}
