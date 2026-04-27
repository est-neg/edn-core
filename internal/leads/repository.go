package leads

import (
	"context"
)

// Repository is the persistence port for the leads module.
type Repository interface {
	Save(ctx context.Context, lead Lead) error
	ExistsByEmail(ctx context.Context, email string) (bool, error)
	ExistsByPhone(ctx context.Context, phone string) (bool, error)
	UpdateNotificationStatus(ctx context.Context, leadID, status, detail string) error
}
