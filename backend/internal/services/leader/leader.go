// Package leader wraps Kubernetes lease-based leader election so that
// control-plane singleton background loops (the K8s sync loop, Team event
// consumers and the stale-task monitor) run on exactly one clawmanager-app
// replica at a time. The HTTP API and the in-pod nginx desktop data plane run
// on every replica; only these background loops must be gated.
package leader

import (
	"context"
	"log"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// Config holds the resolved leader-election parameters.
type Config struct {
	Namespace     string
	LeaseName     string
	Identity      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

// Callbacks are invoked when this replica acquires or loses leadership.
//
// OnStartedLeading receives a context that is cancelled when leadership is
// lost; callers may use it to scope leader-only work. OnStoppedLeading is
// invoked when leadership is lost (or the parent context is cancelled) and must
// stop any work started in OnStartedLeading. Both callbacks must be safe to
// call repeatedly across leadership transitions.
type Callbacks struct {
	OnStartedLeading func(ctx context.Context)
	OnStoppedLeading func()
}

// Run participates in leader election and blocks until ctx is cancelled. It is
// resilient to leadership loss: after losing the lease it re-participates so a
// replica that briefly lost leadership can re-acquire it and restart the
// background loops. Run is intended to be called in its own goroutine.
func Run(ctx context.Context, clientset kubernetes.Interface, cfg Config, cb Callbacks) {
	identity := cfg.Identity
	if identity == "" {
		if host, err := os.Hostname(); err == nil && host != "" {
			identity = host
		} else {
			identity = "clawmanager-app"
		}
	}

	leaseDuration := cfg.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 15 * time.Second
	}
	renewDeadline := cfg.RenewDeadline
	if renewDeadline <= 0 || renewDeadline >= leaseDuration {
		renewDeadline = leaseDuration * 2 / 3
	}
	retryPeriod := cfg.RetryPeriod
	if retryPeriod <= 0 {
		retryPeriod = 2 * time.Second
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaseName,
			Namespace: cfg.Namespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	}

	electionCfg := leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   leaseDuration,
		RenewDeadline:   renewDeadline,
		RetryPeriod:     retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(c context.Context) {
				log.Printf("[leader] acquired leadership (identity=%s lease=%s/%s)", identity, cfg.Namespace, cfg.LeaseName)
				if cb.OnStartedLeading != nil {
					cb.OnStartedLeading(c)
				}
			},
			OnStoppedLeading: func() {
				log.Printf("[leader] lost leadership (identity=%s)", identity)
				if cb.OnStoppedLeading != nil {
					cb.OnStoppedLeading()
				}
			},
		},
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		elector, err := leaderelection.NewLeaderElector(electionCfg)
		if err != nil {
			log.Printf("[leader] failed to create leader elector: %v; retrying in %s", err, retryPeriod)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryPeriod):
				continue
			}
		}

		// Blocks until leadership is lost or ctx is cancelled. On loss we loop
		// and re-participate.
		elector.Run(ctx)
	}
}
