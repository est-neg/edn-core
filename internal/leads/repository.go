package leads

import (
	"context"
	"time"
)

// Repository is the persistence port for the leads module.
type Repository interface {
	Save(ctx context.Context, lead Lead) error
	ExistsByWhatsAppAndProfile(ctx context.Context, whatsapp, profile string, since time.Time) (bool, error)
	UpdateNotificationStatus(ctx context.Context, leadID, status, detail string) error
}
