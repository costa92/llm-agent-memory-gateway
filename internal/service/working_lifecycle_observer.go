package service

import "context"

type WorkingLifecycleObservation struct {
	TenantID         string
	Mode             string
	Expired          int
	DroppedBeforeUse int
	Promoted         int
}

type WorkingLifecycleObserver interface {
	ObserveWorkingLifecycle(ctx context.Context, obs WorkingLifecycleObservation)
}

type nopWorkingLifecycleObserver struct{}

func (nopWorkingLifecycleObserver) ObserveWorkingLifecycle(context.Context, WorkingLifecycleObservation) {
}

func resolveWorkingLifecycleObserver(observer WorkingLifecycleObserver) WorkingLifecycleObserver {
	if observer != nil {
		return observer
	}
	return nopWorkingLifecycleObserver{}
}
