package application

import (
	"context"
	"time"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

func Recover(ctx context.Context, store ports.SessionStore, epoch string, now time.Time) error {
	return store.Reconcile(ctx, epoch, now)
}
