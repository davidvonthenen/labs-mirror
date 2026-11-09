package platform

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ReadinessPendingError marks a transient data-plane failure. Configuration,
// credentials, TLS trust, and malformed responses must not use this type.
type ReadinessPendingError struct {
	Message    string
	RetryAfter time.Duration
}

func (e *ReadinessPendingError) Error() string { return e.Message }

// WaitResponsive waits for a usable endpoint and a successful authenticated
// probe, not every management node's RUNNING state. In particular, Dashboards
// provisioning and health do not gate OpenSearch data-plane readiness.
func (p *OpenSearch) WaitResponsive(ctx context.Context, id string, probe func(context.Context, Cluster) error) (Cluster, error) {
	if probe == nil {
		return Cluster{}, errors.New("OpenSearch readiness requires a data-plane probe")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	var last error
	var c Cluster
	for {
		if ctx.Err() != nil {
			return c, readinessWaitError(ctx.Err(), last)
		}
		var err error
		c, err = p.Get(ctx, id)
		if ctx.Err() != nil {
			return c, readinessWaitError(ctx.Err(), last)
		}
		if err == nil {
			switch c.Status {
			case "FAILED", "ERROR", "DELETED", "DELETING":
				return c, errors.New("OpenSearch cluster failed or is being deleted; inspect the Instaclustr Console and retain the ownership record")
			}
			_, err = c.Connection()
			if errors.Is(err, ErrConnectionUnavailable) {
				err = &ReadinessPendingError{Message: err.Error()}
			} else if err == nil {
				err = probe(ctx, c)
				if err == nil {
					return c, nil
				}
			}
		}
		if ctx.Err() != nil {
			return c, readinessWaitError(ctx.Err(), err)
		}
		var pending *ReadinessPendingError
		var transport *requestError
		if !errors.As(err, &pending) && !errors.As(err, &transport) && !retryableRead(err) {
			return c, err
		}
		last = err
		// Reuse the provider's bounded, cancellation-aware polling, including
		// Retry-After for either the management or data-plane HTTP response.
		pauseCause := err
		if pending != nil && pending.RetryAfter > 0 {
			pauseCause = &HTTPError{RetryAfter: pending.RetryAfter}
		}
		if err := p.pause(ctx, pauseCause); err != nil {
			return c, readinessWaitError(err, last)
		}
	}
}

func readinessWaitError(cause, last error) error {
	if last == nil {
		return fmt.Errorf("waiting for OpenSearch data-plane readiness: %w", cause)
	}
	return fmt.Errorf("waiting for OpenSearch data-plane readiness: %w; last check: %s", cause, last)
}
