package controller

// UNIFYAPI-FORK: periodically snapshot the billing configuration.
//
// model.UpdateOption snapshots a billing key before overwriting it, which covers
// every change made through the application. It does NOT cover a write that
// never runs Go -- raw SQL over SSM, a migration, a restored dump.
//
// That distinction is not theoretical. The two writes that destroyed an
// operator's discount configuration on 2026-08-29 and 2026-08-30 were both raw
// SQL over SSM, so no application hook would have seen either. Both commands did
// take ad-hoc backups; the failure was that nobody knew the files existed or
// where to look. A snapshot with a known location, a known schedule and a
// documented restore path is the fix. Another .tsv in someone's home directory
// is not.
//
// So this runs on a timer and records the current value of each billing key
// whenever it differs from the last one on record, whoever changed it and
// however they did it. It cannot say who -- that is the price of catching a
// change that bypassed the app entirely -- but it can always say what the value
// was, which is the half that matters when it is your own configuration that
// vanished.

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type pricingSnapshotHandler struct{}

func (pricingSnapshotHandler) Type() string { return model.SystemTaskTypePricingSnapshot }

// Enabled defaults to ON and has no off switch in the UI on purpose. The run is
// a handful of small reads and, on an unchanged configuration, zero writes. A
// safety net that can be switched off is one that is off on the day it was
// needed.
func (pricingSnapshotHandler) Enabled() bool { return true }

// Interval is deliberately short relative to how often pricing changes. The
// snapshot is skipped entirely when nothing has changed, so the cost of waking
// often is a few indexed reads, while the cost of waking rarely is measured in
// however long an out-of-band change goes unrecorded.
func (pricingSnapshotHandler) Interval() time.Duration { return time.Hour }

func (pricingSnapshotHandler) NewPayload() any { return nil }

func (h pricingSnapshotHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	recorded, err := model.SnapshotBillingConfig("scheduled")
	if err != nil {
		common.SysError("pricing snapshot: " + err.Error())
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}

	// Logged only when something changed, so a quiet configuration produces a
	// quiet log and the lines that do appear are all worth reading.
	if recorded > 0 {
		common.SysLog(fmt.Sprintf(
			"pricing snapshot: recorded %d billing config value(s) that changed outside the console", recorded))
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded,
		map[string]any{"recorded": recorded}, nil)
}
